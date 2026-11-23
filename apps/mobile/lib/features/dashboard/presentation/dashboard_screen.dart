import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import '../../../core/network/broker_websocket_client.dart';
import '../../../core/security/secure_storage.dart';
import '../../machines/presentation/qr_pairing_dialog.dart';

class DashboardScreen extends StatefulWidget {
  final String? initialBrokerUrl;
  final String? machineId;
  final String? deviceId;
  final String? sessionId;
  final String? token;
  final String? masterSecret;
  final SecureStorageService? secureStorage;

  const DashboardScreen({
    super.key,
    this.initialBrokerUrl,
    this.machineId,
    this.deviceId,
    this.sessionId,
    this.token,
    this.masterSecret,
    this.secureStorage,
  });

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  late TextEditingController _brokerController;
  BrokerWebSocketClient? _brokerClient;
  BrokerConnectionState _connectionState = BrokerConnectionState.disconnected;
  bool _isLoading = false;
  Map<String, dynamic>? _healthData;
  List<dynamic> _workspaces = [];
  String? _errorMessage;

  String? _machineId;
  String? _deviceId;
  String? _sessionId;
  String? _token;
  String? _masterSecret;
  late final SecureStorageService _storage;

  bool get hasValidCredentials =>
      _machineId != null &&
      _machineId!.isNotEmpty &&
      _deviceId != null &&
      _deviceId!.isNotEmpty &&
      _sessionId != null &&
      _sessionId!.isNotEmpty &&
      _token != null &&
      _token!.isNotEmpty &&
      _masterSecret != null &&
      _masterSecret!.isNotEmpty;

  @override
  void initState() {
    super.initState();
    _storage = widget.secureStorage ?? SecureStorageService.instance;
    _machineId = widget.machineId;
    _deviceId = widget.deviceId;
    _sessionId = widget.sessionId;
    _token = widget.token;
    _masterSecret = widget.masterSecret;

    final defaultUrl = widget.initialBrokerUrl ?? '';
    _brokerController = TextEditingController(text: defaultUrl);

    _initCredentialsAndConnect(defaultUrl);
  }

  Future<void> _initCredentialsAndConnect(String defaultUrl) async {
    if (!hasValidCredentials) {
      final session = await _storage.getPairedSession();
      if (session != null && mounted) {
        setState(() {
          _machineId = session['machineId'];
          _deviceId = session['deviceId'];
          _sessionId = session['sessionId'];
          _token = session['token'];
          _masterSecret = session['masterSecret'];
          if (_brokerController.text.isEmpty && session['brokerUrl'] != null) {
            _brokerController.text = session['brokerUrl']!;
          }
        });
      }
    }

    if (!hasValidCredentials) {
      if (mounted) {
        setState(() {
          _errorMessage = 'Pairing Required: No authenticated host session credentials available. Pair a host machine to connect.';
        });
      }
      return;
    }

    if (defaultUrl.isNotEmpty) {
      _connectToBroker();
    }
  }

  Future<void> _openPairingDialog() async {
    final success = await showDialog<bool>(
      context: context,
      builder: (context) => QrPairingDialog(secureStorage: _storage),
    );

    if (success == true && mounted) {
      final session = await _storage.getPairedSession();
      if (session != null && mounted) {
        setState(() {
          _machineId = session['machineId'];
          _deviceId = session['deviceId'];
          _sessionId = session['sessionId'];
          _token = session['token'];
          _masterSecret = session['masterSecret'];
          if (session['brokerUrl'] != null && session['brokerUrl']!.isNotEmpty) {
            _brokerController.text = session['brokerUrl']!;
          }
          _errorMessage = null;
        });

        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Host Machine Paired Successfully!'),
            backgroundColor: Colors.green,
          ),
        );

        if (_brokerController.text.trim().isNotEmpty) {
          _connectToBroker();
        }
      } else if (mounted) {
        setState(() {
          _errorMessage = 'Pairing verification failed: no valid session found in secure storage.';
        });
      }
    }
  }

  Future<void> _handleLogout() async {
    _brokerClient?.disconnect();
    _brokerClient?.dispose();
    _brokerClient = null;

    await _storage.clearPairedSession();

    if (mounted) {
      setState(() {
        _machineId = null;
        _deviceId = null;
        _sessionId = null;
        _token = null;
        _masterSecret = null;
        _connectionState = BrokerConnectionState.disconnected;
        _healthData = null;
        _workspaces = [];
        _errorMessage = 'Pairing Required: No authenticated host session credentials available. Pair a host machine to connect.';
      });

      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Device unpaired. Session credentials cleared from secure storage.'),
        ),
      );
    }
  }

  Future<void> _connectToBroker() async {
    if (!hasValidCredentials) {
      if (mounted) {
        setState(() {
          _errorMessage = 'Authentication Required: Cannot initiate authenticated session without valid paired credentials.';
        });
      }
      return;
    }

    final url = _brokerController.text.trim();
    if (url.isEmpty) {
      if (mounted) {
        setState(() {
          _errorMessage = 'Please enter a valid Broker WSS URL (e.g. ws://127.0.0.1:8081).';
        });
      }
      return;
    }

    _brokerClient?.disconnect();
    _brokerClient?.dispose();

    _brokerClient = BrokerWebSocketClient(
      brokerUrl: url,
      machineId: _machineId!,
      deviceId: _deviceId!,
      sessionId: _sessionId!,
      token: _token!,
      masterSecret: _masterSecret!,
    );

    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    Timer(const Duration(seconds: 4), () {
      if (mounted && _isLoading && _connectionState != BrokerConnectionState.authenticatedBrokerSessionActive) {
        setState(() {
          _isLoading = false;
        });
      }
    });

    _brokerClient!.stateStream.listen((state) async {
      if (mounted) {
        setState(() {
          _connectionState = state;
          _isLoading = (state == BrokerConnectionState.connecting ||
                        state == BrokerConnectionState.authenticating ||
                        state == BrokerConnectionState.reconnecting);
        });
      }

      if (state == BrokerConnectionState.authenticatedBrokerSessionActive) {
        await _fetchLiveHostDataOverBroker();
      } else if (state == BrokerConnectionState.waitingForHost) {
        if (mounted) {
          setState(() {
            _isLoading = false;
            _errorMessage = 'Host offline: Waiting for authorized host to connect to broker...';
          });
        }
      } else if (state == BrokerConnectionState.hostDisconnected) {
        if (mounted) {
          setState(() {
            _isLoading = false;
            _errorMessage = 'Host disconnected: Waiting for host reconnection...';
          });
        }
      } else if (state == BrokerConnectionState.sessionExpired) {
        await _storage.clearPairedSession();
        if (mounted) {
          setState(() {
            _machineId = null;
            _deviceId = null;
            _sessionId = null;
            _token = null;
            _masterSecret = null;
            _isLoading = false;
            _errorMessage = 'Session expired: Session exceeded valid lifetime. Please re-pair.';
          });
        }
      } else if (state == BrokerConnectionState.revoked) {
        await _storage.clearPairedSession();
        if (mounted) {
          setState(() {
            _machineId = null;
            _deviceId = null;
            _sessionId = null;
            _token = null;
            _masterSecret = null;
            _isLoading = false;
            _errorMessage = 'Device revoked: Credentials invalidated and cleared from secure storage.';
          });
        }
      } else if (state == BrokerConnectionState.authFailed) {
        if (mounted) {
          setState(() {
            _isLoading = false;
            _errorMessage = 'Authentication failed: connection rejected by broker.';
          });
        }
      } else if (state == BrokerConnectionState.connectionFailed) {
        if (mounted) {
          setState(() {
            _isLoading = false;
            _errorMessage = 'Connection failed: Handshake timed out or failed to reach broker.';
          });
        }
      }
    });

    await _brokerClient!.connect();
  }

  Future<void> _fetchLiveHostDataOverBroker() async {
    if (_brokerClient == null || _brokerClient!.state != BrokerConnectionState.authenticatedBrokerSessionActive) {
      return;
    }

    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    try {
      final healthResp = await _brokerClient!.sendRequest(
        method: 'GET',
        path: '/api/v1/health',
      );

      if (healthResp.statusCode == 200) {
        final healthJson = jsonDecode(healthResp.body) as Map<String, dynamic>;

        final wsResp = await _brokerClient!.sendRequest(
          method: 'GET',
          path: '/api/v1/workspaces',
        );

        List<dynamic> wsList = [];
        if (wsResp.statusCode == 200) {
          wsList = jsonDecode(wsResp.body) as List<dynamic>;
        }

        if (mounted) {
          setState(() {
            _healthData = healthJson;
            _workspaces = wsList;
            _isLoading = false;
          });
        }
      } else {
        if (mounted) {
          setState(() {
            _healthData = null;
            _workspaces = [];
            _isLoading = false;
            _errorMessage = 'Broker request failed with status HTTP ${healthResp.statusCode}';
          });
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _healthData = null;
          _workspaces = [];
          _isLoading = false;
          _errorMessage = 'Broker request error: $e';
        });
      }
    }
  }

  @override
  void dispose() {
    _brokerController.dispose();
    _brokerClient?.disconnect();
    _brokerClient?.dispose();
    super.dispose();
  }

  Color _getStatusColor(BrokerConnectionState state) {
    if (!hasValidCredentials) {
      return Colors.orangeAccent;
    }
    switch (state) {
      case BrokerConnectionState.authenticatedBrokerSessionActive:
        return Colors.green;
      case BrokerConnectionState.waitingForHost:
        return Colors.cyan;
      case BrokerConnectionState.hostDisconnected:
        return Colors.deepOrange;
      case BrokerConnectionState.connecting:
      case BrokerConnectionState.transportConnected:
      case BrokerConnectionState.authenticating:
      case BrokerConnectionState.reconnecting:
        return Colors.amber;
      case BrokerConnectionState.sessionExpired:
      case BrokerConnectionState.authFailed:
      case BrokerConnectionState.connectionFailed:
      case BrokerConnectionState.revoked:
      case BrokerConnectionState.disconnected:
        return Colors.red;
    }
  }

  String _getStatusLabel(BrokerConnectionState state) {
    if (!hasValidCredentials) {
      return 'PAIRING REQUIRED';
    }
    switch (state) {
      case BrokerConnectionState.authenticatedBrokerSessionActive:
        return 'AUTHENTICATED BROKER SESSION ACTIVE';
      case BrokerConnectionState.waitingForHost:
        return 'WAITING FOR HOST TO CONNECT';
      case BrokerConnectionState.hostDisconnected:
        return 'HOST DISCONNECTED — WAITING FOR RECONNECT';
      case BrokerConnectionState.connecting:
        return 'CONNECTING TO BROKER';
      case BrokerConnectionState.transportConnected:
        return 'TRANSPORT CONNECTED';
      case BrokerConnectionState.authenticating:
        return 'AUTHENTICATING';
      case BrokerConnectionState.reconnecting:
        return 'RECONNECTING';
      case BrokerConnectionState.sessionExpired:
        return 'SESSION EXPIRED';
      case BrokerConnectionState.authFailed:
        return 'AUTHENTICATION FAILED';
      case BrokerConnectionState.connectionFailed:
        return 'CONNECTION FAILED';
      case BrokerConnectionState.revoked:
        return 'DEVICE REVOKED';
      case BrokerConnectionState.disconnected:
        return 'DISCONNECTED';
    }
  }

  @override
  Widget build(BuildContext context) {
    final hostname = _healthData?['hostname'] ?? 'Desktop Host';
    final osType = _healthData?['os'] ?? 'Host';
    final arch = _healthData?['arch'] ?? '';
    final version = _healthData?['host_bridge_version'] != null ? 'Host Bridge ${_healthData!["host_bridge_version"]}' : '';
    final allowedRoots = _healthData?['allowed_workspaces_count'] ?? 0;
    final isOnline = _connectionState == BrokerConnectionState.authenticatedBrokerSessionActive;

    return RefreshIndicator(
      onRefresh: _fetchLiveHostDataOverBroker,
      child: ListView(
        padding: const EdgeInsets.all(16.0),
        children: [
          // Pairing Required Warning Banner when credentials unavailable
          if (!hasValidCredentials) ...[
            Card(
              color: Colors.amber.shade900.withValues(alpha: 0.25),
              child: Padding(
                padding: const EdgeInsets.all(16.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Row(
                      children: [
                        Icon(Icons.warning_amber_rounded, color: Colors.amberAccent, size: 28),
                        SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            'Host Pairing Required',
                            style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold, color: Colors.white),
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      'No authenticated host session credentials found. Pairing with your desktop host bridge via QR code is required before an authenticated broker connection can be established.',
                      style: TextStyle(fontSize: 13, color: Colors.white70),
                    ),
                    const SizedBox(height: 12),
                    ElevatedButton.icon(
                      onPressed: _openPairingDialog,
                      icon: const Icon(Icons.qr_code_scanner),
                      label: const Text('Pair Host Machine (Scan QR)'),
                      style: ElevatedButton.styleFrom(
                        backgroundColor: Colors.purpleAccent,
                        foregroundColor: Colors.white,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 12),
          ],

          // Connection Broker Endpoint Selection Input Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(12.0),
              child: Row(
                children: [
                  const Icon(Icons.hub, color: Colors.purpleAccent),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _brokerController,
                      enabled: hasValidCredentials,
                      decoration: InputDecoration(
                        labelText: 'Broker WSS URL',
                        hintText: hasValidCredentials ? 'e.g. ws://localhost:8081' : 'Pair host machine first',
                        isDense: true,
                        border: const OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: !hasValidCredentials ? null : _connectToBroker,
                    child: _isLoading
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                          )
                        : const Text('Connect'),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // Real Authenticated Session Health Status Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.center,
                    children: [
                      Icon(
                        Icons.computer,
                        color: _getStatusColor(_connectionState),
                        size: 28,
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Text(
                          !hasValidCredentials
                              ? 'Host Session (Unpaired)'
                              : isOnline
                                  ? '$hostname ($osType/$arch)'
                                  : 'Host Session Inactive',
                          style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 15),
                        ),
                      ),
                      if (hasValidCredentials) ...[
                        IconButton(
                          icon: const Icon(Icons.link_off, color: Colors.redAccent, size: 20),
                          tooltip: 'Unpair / Logout Session',
                          onPressed: _handleLogout,
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 8),
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                    decoration: BoxDecoration(
                      color: _getStatusColor(_connectionState).withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: _getStatusColor(_connectionState).withValues(alpha: 0.4)),
                    ),
                    child: Text(
                      _getStatusLabel(_connectionState),
                      style: TextStyle(
                        color: _getStatusColor(_connectionState),
                        fontSize: 11,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                  ),
                  const SizedBox(height: 8),
                  Text(
                    !hasValidCredentials
                        ? 'Pair host machine to enable authenticated remote IDE connection.'
                        : isOnline
                            ? '${version.isNotEmpty ? "$version • " : ""}Via Broker WSS • Allowed Roots: $allowedRoots'
                            : (_errorMessage ?? _getStatusLabel(_connectionState)),
                    style: TextStyle(fontSize: 12, color: Colors.grey.shade400),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // Real Discovered Projects Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        'Live Discovered Projects',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      IconButton(
                        icon: const Icon(Icons.refresh, size: 20),
                        onPressed: hasValidCredentials ? _fetchLiveHostDataOverBroker : null,
                        tooltip: 'Refresh Host Status',
                      ),
                    ],
                  ),
                  const Divider(),
                  if (_isLoading)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else if (!hasValidCredentials)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text(
                        'Device not paired. Pair with Host Bridge to view live projects.',
                        style: TextStyle(color: Colors.grey),
                      ),
                    )
                  else if (!isOnline)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text(
                        'Broker session offline. Connect to Connection Broker to view live host projects.',
                        style: TextStyle(color: Colors.grey),
                      ),
                    )
                  else if (_workspaces.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text('No Android Studio or Gradle projects found under host allowed roots.'),
                    )
                  else
                    ..._workspaces.map((ws) {
                      final name = ws['name'] ?? 'Project';
                      final rootPath = ws['root_path'] ?? '';
                      final isAndroid = ws['is_android_project'] == true;

                      return ListTile(
                        leading: Icon(
                          isAndroid ? Icons.android : Icons.folder,
                          color: isAndroid ? Colors.teal : Colors.amber,
                        ),
                        title: Text(name.toString()),
                        subtitle: Text('Path: $rootPath'),
                        trailing: const Icon(Icons.chevron_right),
                      );
                    }),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

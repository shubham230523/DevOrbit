import 'dart:convert';
import 'package:flutter/material.dart';
import '../../../core/network/broker_websocket_client.dart';

class DashboardScreen extends StatefulWidget {
  final String? initialBrokerUrl;
  final String machineId;
  final String deviceId;
  final String sessionId;
  final String token;
  final String masterSecret;

  const DashboardScreen({
    super.key,
    this.initialBrokerUrl,
    this.machineId = 'host-windows-01',
    this.deviceId = 'phone-android-01',
    this.sessionId = 'sess-e2e-100',
    this.token = 'valid-client-token-32b',
    this.masterSecret = 'devorbit-master-secret-key-32b!',
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

  @override
  void initState() {
    super.initState();
    final defaultUrl = widget.initialBrokerUrl ?? '';
    _brokerController = TextEditingController(text: defaultUrl);
    if (defaultUrl.isNotEmpty) {
      _connectToBroker();
    }
  }

  Future<void> _connectToBroker() async {
    final url = _brokerController.text.trim();
    if (url.isEmpty) return;

    _brokerClient?.disconnect();
    _brokerClient?.dispose();

    _brokerClient = BrokerWebSocketClient(
      brokerUrl: url,
      machineId: widget.machineId,
      deviceId: widget.deviceId,
      sessionId: widget.sessionId,
      token: widget.token,
      masterSecret: widget.masterSecret,
    );

    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    _brokerClient!.stateStream.listen((state) {
      if (mounted) {
        setState(() {
          _connectionState = state;
        });
      }
    });

    await _brokerClient!.connect();
    if (_brokerClient!.state == BrokerConnectionState.authenticatedBrokerSessionActive) {
      await _fetchLiveHostDataOverBroker();
    } else {
      if (mounted) {
        setState(() {
          _isLoading = false;
          _errorMessage = 'Failed to establish authenticated session with Connection Broker at $url';
        });
      }
    }
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
    switch (state) {
      case BrokerConnectionState.authenticatedBrokerSessionActive:
        return Colors.green;
      case BrokerConnectionState.connecting:
      case BrokerConnectionState.authenticating:
      case BrokerConnectionState.reconnecting:
        return Colors.amber;
      case BrokerConnectionState.authFailed:
      case BrokerConnectionState.revoked:
      case BrokerConnectionState.disconnected:
        return Colors.red;
    }
  }

  String _getStatusLabel(BrokerConnectionState state) {
    switch (state) {
      case BrokerConnectionState.authenticatedBrokerSessionActive:
        return 'AUTHENTICATED BROKER SESSION ACTIVE';
      case BrokerConnectionState.connecting:
        return 'CONNECTING TO BROKER';
      case BrokerConnectionState.authenticating:
        return 'AUTHENTICATING';
      case BrokerConnectionState.reconnecting:
        return 'RECONNECTING';
      case BrokerConnectionState.authFailed:
        return 'AUTHENTICATION FAILED';
      case BrokerConnectionState.revoked:
        return 'DEVICE REVOKED';
      case BrokerConnectionState.disconnected:
        return 'DISCONNECTED';
    }
  }

  @override
  Widget build(BuildContext context) {
    final hostname = _healthData?['hostname'] ?? 'Desktop Host';
    final osType = _healthData?['os'] ?? 'Unknown OS';
    final arch = _healthData?['arch'] ?? '';
    final version = _healthData?['host_bridge_version'] ?? 'v1.2.1';
    final allowedRoots = _healthData?['allowed_workspaces_count'] ?? 0;
    final isOnline = _connectionState == BrokerConnectionState.authenticatedBrokerSessionActive;

    return RefreshIndicator(
      onRefresh: _fetchLiveHostDataOverBroker,
      child: ListView(
        padding: const EdgeInsets.all(16.0),
        children: [
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
                      decoration: const InputDecoration(
                        labelText: 'Connection Broker WSS Endpoint',
                        hintText: 'e.g. ws://localhost:8081',
                        isDense: true,
                        border: OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: _isLoading ? null : _connectToBroker,
                    child: const Text('Connect'),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // Real Authenticated Session Health Status Card
          Card(
            child: ListTile(
              leading: Icon(
                Icons.computer,
                color: _getStatusColor(_connectionState),
                size: 32,
              ),
              title: Text(
                isOnline ? '$hostname ($osType/$arch)' : 'Host Session Inactive',
                style: const TextStyle(fontWeight: FontWeight.bold),
              ),
              subtitle: isOnline
                  ? Text('Host Bridge $version • Via Broker WSS\nAllowed Roots: $allowedRoots')
                  : Text(_errorMessage ?? _getStatusLabel(_connectionState)),
              isThreeLine: isOnline,
              trailing: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Chip(
                    label: Text(_getStatusLabel(_connectionState)),
                    backgroundColor: _getStatusColor(_connectionState).withValues(alpha: 0.2),
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
                        onPressed: _fetchLiveHostDataOverBroker,
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

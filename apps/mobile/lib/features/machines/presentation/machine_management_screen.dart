import 'package:flutter/material.dart';
import '../../../core/security/secure_storage.dart';
import 'qr_pairing_dialog.dart';

class MachineManagementScreen extends StatefulWidget {
  final SecureStorageService? secureStorage;

  const MachineManagementScreen({super.key, this.secureStorage});

  @override
  State<MachineManagementScreen> createState() => _MachineManagementScreenState();
}

class _MachineManagementScreenState extends State<MachineManagementScreen> {
  bool _isLanMode = false;
  Map<String, String>? _pairedSession;
  late final SecureStorageService _storage;

  @override
  void initState() {
    super.initState();
    _storage = widget.secureStorage ?? SecureStorageService.instance;
    _loadSession();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _loadSession();
  }

  Future<void> _loadSession() async {
    final session = await _storage.getPairedSession();
    if (mounted) {
      setState(() {
        _pairedSession = session;
      });
    }
  }

  void _showPairingDialog() async {
    final success = await showDialog<bool>(
      context: context,
      builder: (context) => QrPairingDialog(secureStorage: _storage),
    );

    if (success == true && mounted) {
      await _loadSession();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Host Machine Paired & Identity Verified!'),
            backgroundColor: Colors.green,
          ),
        );
      }
    }
  }

  Future<void> _handleRevoke() async {
    await _storage.clearPairedSession();
    if (mounted) {
      setState(() {
        _pairedSession = null;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Host machine trust revoked. Paired session cleared.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final machineId = _pairedSession?['machineId'] ?? '';
    final deviceId = _pairedSession?['deviceId'] ?? '';
    final brokerUrl = _pairedSession?['brokerUrl'] ?? '';
    final isPaired = _pairedSession != null && machineId.isNotEmpty;

    return ListView(
      padding: const EdgeInsets.all(16.0),
      children: [
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16.0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Connection Mode',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(height: 10),
                SizedBox(
                  width: double.infinity,
                  child: SegmentedButton<bool>(
                    segments: const [
                      ButtonSegment(
                        value: false,
                        label: Text('Broker (WSS)'),
                        icon: Icon(Icons.cloud_sync, size: 18),
                      ),
                      ButtonSegment(
                        value: true,
                        label: Text('LAN (mTLS)'),
                        icon: Icon(Icons.wifi, size: 18),
                      ),
                    ],
                    selected: {_isLanMode},
                    onSelectionChanged: (set) {
                      setState(() {
                        _isLanMode = set.first;
                      });
                    },
                  ),
                ),
                const SizedBox(height: 10),
                Text(
                  _isLanMode
                      ? 'Direct mutual TLS connection on local Wi-Fi network.'
                      : 'End-to-End encrypted tunnel routed via DevOrbit Connection Broker.',
                  style: const TextStyle(fontSize: 12, color: Colors.grey),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 12),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16.0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(
                      Icons.laptop_windows,
                      color: isPaired ? Colors.green : Colors.grey,
                      size: 28,
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(
                        isPaired ? 'Paired Host Machine ($machineId)' : 'No Host Machine Paired',
                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 15),
                      ),
                    ),
                    if (isPaired)
                      PopupMenuButton<String>(
                        onSelected: (val) {
                          if (val == 'revoke') {
                            _handleRevoke();
                          }
                        },
                        itemBuilder: (context) => [
                          const PopupMenuItem(
                            value: 'revoke',
                            child: Text('Revoke Machine Trust', style: TextStyle(color: Colors.redAccent)),
                          ),
                        ],
                      ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  isPaired
                      ? 'Device ID: $deviceId\nBroker URL: ${brokerUrl.isNotEmpty ? brokerUrl : "Default Broker"}\nStatus: Authenticated Paired Identity'
                      : 'Pair with your workstation host bridge via QR code scan to enable remote AI coding.',
                  style: TextStyle(fontSize: 12, color: Colors.grey.shade400),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 16),
        ElevatedButton.icon(
          onPressed: _showPairingDialog,
          icon: const Icon(Icons.qr_code_scanner),
          label: Text(isPaired ? 'Re-Pair / Scan New Host QR' : 'Pair Host Machine (Scan QR)'),
          style: ElevatedButton.styleFrom(
            padding: const EdgeInsets.symmetric(vertical: 16.0),
            backgroundColor: Colors.purpleAccent,
            foregroundColor: Colors.white,
          ),
        ),
      ],
    );
  }
}


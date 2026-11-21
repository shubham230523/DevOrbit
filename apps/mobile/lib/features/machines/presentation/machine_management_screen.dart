import 'package:flutter/material.dart';
import 'qr_pairing_dialog.dart';

class MachineManagementScreen extends StatefulWidget {
  const MachineManagementScreen({super.key});

  @override
  State<MachineManagementScreen> createState() => _MachineManagementScreenState();
}

class _MachineManagementScreenState extends State<MachineManagementScreen> {
  bool _isLanMode = true;
  bool _isConnected = true;

  void _showPairingDialog() async {
    final success = await showDialog<bool>(
      context: context,
      builder: (context) => const QrPairingDialog(),
    );

    if (success == true && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Host Machine Paired & Identity Verified!'),
          backgroundColor: Colors.green,
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16.0),
      children: [
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
                      'Connection Mode',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                    SegmentedButton<bool>(
                      segments: const [
                        ButtonSegment(
                          value: true,
                          label: Text('LAN (mTLS)'),
                          icon: Icon(Icons.wifi),
                        ),
                        ButtonSegment(
                          value: false,
                          label: Text('Broker (WSS)'),
                          icon: Icon(Icons.cloud_sync),
                        ),
                      ],
                      selected: {_isLanMode},
                      onSelectionChanged: (set) {
                        setState(() {
                          _isLanMode = set.first;
                        });
                      },
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  _isLanMode
                      ? 'Direct mutual TLS connection on local Wi-Fi.'
                      : 'End-to-End encrypted tunnel routed via DevOrbit Connection Broker.',
                  style: const TextStyle(fontSize: 12, color: Colors.grey),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 12),
        Card(
          child: ListTile(
            leading: Icon(
              Icons.laptop_windows,
              color: _isConnected ? Colors.green : Colors.grey,
            ),
            title: const Text('Windows Host 01'),
            subtitle: Text(
              _isLanMode
                  ? 'Host: 192.168.1.100:8082 • Direct mTLS Active'
                  : 'Host: DevOrbit Broker Tunnel • E2E Encrypted Active',
            ),
            trailing: PopupMenuButton<String>(
              onSelected: (val) {
                if (val == 'disconnect') {
                  setState(() {
                    _isConnected = !_isConnected;
                  });
                }
              },
              itemBuilder: (context) => [
                PopupMenuItem(
                  value: 'disconnect',
                  child: Text(_isConnected ? 'Disconnect' : 'Connect'),
                ),
                const PopupMenuItem(
                  value: 'revoke',
                  child: Text('Revoke Machine Trust', style: TextStyle(color: Colors.redAccent)),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 16),
        ElevatedButton.icon(
          onPressed: _showPairingDialog,
          icon: const Icon(Icons.qr_code_scanner),
          label: const Text('Pair New Host Machine (Scan QR)'),
          style: ElevatedButton.styleFrom(
            padding: const EdgeInsets.symmetric(vertical: 16.0),
          ),
        ),
      ],
    );
  }
}

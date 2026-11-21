import 'package:flutter/material.dart';

class MachineManagementScreen extends StatelessWidget {
  const MachineManagementScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16.0),
      children: [
        Card(
          child: ListTile(
            leading: const Icon(Icons.laptop_windows, color: Colors.blue),
            title: const Text('Windows Host 01'),
            subtitle: const Text('Host: 192.168.1.100 • LAN mTLS Ready'),
            trailing: IconButton(
              icon: const Icon(Icons.qr_code_scanner),
              onPressed: () {},
            ),
          ),
        ),
        const SizedBox(height: 12),
        ElevatedButton.icon(
          onPressed: () {},
          icon: const Icon(Icons.add_a_photo),
          label: const Text('Pair New Host Machine (Scan QR)'),
        ),
      ],
    );
  }
}

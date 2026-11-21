import 'package:flutter/material.dart';

class DashboardScreen extends StatelessWidget {
  const DashboardScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16.0),
      children: [
        Card(
          child: ListTile(
            leading: const Icon(Icons.computer, color: Colors.green),
            title: const Text('Dev Host: Linux Workstation 01'),
            subtitle: const Text('Status: Connected (mTLS LAN) • Latency: 4ms'),
            trailing: Chip(
              label: const Text('Active'),
              backgroundColor: Colors.green.withValues(alpha: 0.2),
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
                Text(
                  'Recent Android Studio Projects',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const Divider(),
                const ListTile(
                  leading: Icon(Icons.android, color: Colors.teal),
                  title: Text('DevOrbit'),
                  subtitle: Text('Path: /home/user/projects/DevOrbit'),
                  trailing: Icon(Icons.chevron_right),
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
                Text(
                  'AI Agent Sessions',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const Divider(),
                const ListTile(
                  leading: Icon(Icons.auto_awesome, color: Colors.amber),
                  title: Text('Google Antigravity Agent'),
                  subtitle: Text('Model: Gemini 3.8 Flash • Session: Active'),
                  trailing: Icon(Icons.chevron_right),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

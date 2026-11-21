import 'package:flutter/material.dart';

class ProjectExplorerScreen extends StatelessWidget {
  const ProjectExplorerScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      children: const [
        ListTile(
          leading: Icon(Icons.folder, color: Colors.amber),
          title: Text('apps/mobile'),
          subtitle: Text('Flutter Application'),
        ),
        ListTile(
          leading: Icon(Icons.folder, color: Colors.blue),
          title: Text('agents/host-bridge'),
          subtitle: Text('Go Host Daemon'),
        ),
        ListTile(
          leading: Icon(Icons.folder, color: Colors.purple),
          title: Text('services/control-plane'),
          subtitle: Text('Go Control Service'),
        ),
        ListTile(
          leading: Icon(Icons.description, color: Colors.grey),
          title: Text('README.md'),
          subtitle: Text('Documentation'),
        ),
      ],
    );
  }
}

import 'package:flutter/material.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      children: [
        SwitchListTile(
          title: const Text('Require Biometrics for Host Access'),
          subtitle: const Text('Android Fingerprint / Apple FaceID'),
          value: true,
          onChanged: (val) {},
        ),
        SwitchListTile(
          title: const Text('Require Confirmation for AI Tool Execution'),
          subtitle: const Text('Prompt before file edits or shell commands'),
          value: true,
          onChanged: (val) {},
        ),
        const ListTile(
          title: Text('DevOrbit Version'),
          subtitle: Text('1.0.0 (Stage 0 Foundation)'),
        ),
      ],
    );
  }
}

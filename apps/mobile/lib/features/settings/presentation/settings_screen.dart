import 'package:flutter/material.dart';
import '../../../core/security/secure_storage.dart';

class SettingsScreen extends StatefulWidget {
  final SecureStorageService? secureStorage;

  const SettingsScreen({super.key, this.secureStorage});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  bool _requireBiometrics = true;
  bool _requireAiConfirmation = true;
  late final SecureStorageService _storage;

  @override
  void initState() {
    super.initState();
    _storage = widget.secureStorage ?? SecureStorageService.instance;
    _loadSettings();
  }

  Future<void> _loadSettings() async {
    final bioVal = await _storage.read('setting_biometrics');
    final aiVal = await _storage.read('setting_ai_confirmation');
    if (mounted) {
      setState(() {
        if (bioVal != null) _requireBiometrics = bioVal == 'true';
        if (aiVal != null) _requireAiConfirmation = aiVal == 'true';
      });
    }
  }

  Future<void> _toggleBiometrics(bool val) async {
    setState(() {
      _requireBiometrics = val;
    });
    await _storage.write('setting_biometrics', val.toString());
  }

  Future<void> _toggleAiConfirmation(bool val) async {
    setState(() {
      _requireAiConfirmation = val;
    });
    await _storage.write('setting_ai_confirmation', val.toString());
  }

  Future<void> _clearAllStorage() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Clear All Session Data?'),
        content: const Text('This will remove all paired host session tokens and keys from secure storage. You will need to re-pair via QR code.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            style: TextButton.styleFrom(foregroundColor: Colors.redAccent),
            child: const Text('Clear & Unpair'),
          ),
        ],
      ),
    );

    if (confirmed == true && mounted) {
      await _storage.clearPairedSession();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('All paired session data cleared from secure storage.'),
            backgroundColor: Colors.orange,
          ),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8.0),
      children: [
        SwitchListTile(
          title: const Text('Require Biometrics for Host Access'),
          subtitle: const Text('Android Fingerprint / Apple FaceID authentication'),
          value: _requireBiometrics,
          onChanged: _toggleBiometrics,
        ),
        const Divider(),
        SwitchListTile(
          title: const Text('Require Confirmation for AI Tool Execution'),
          subtitle: const Text('Prompt before file edits, creations, or shell commands'),
          value: _requireAiConfirmation,
          onChanged: _toggleAiConfirmation,
        ),
        const Divider(),
        ListTile(
          leading: const Icon(Icons.cleaning_services, color: Colors.redAccent),
          title: const Text('Clear Paired Host Credentials', style: TextStyle(color: Colors.redAccent)),
          subtitle: const Text('Remove stored session tokens and secrets from secure storage'),
          onTap: _clearAllStorage,
        ),
        const Divider(),
        const ListTile(
          leading: Icon(Icons.info_outline, color: Colors.purpleAccent),
          title: Text('DevOrbit Version'),
          subtitle: Text('v1.2.0 (Stage E Travel-First MVP)'),
        ),
      ],
    );
  }
}


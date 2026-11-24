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
  bool _hasGitHubToken = false;
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
    final ghPat = await _storage.read('github_pat');
    if (mounted) {
      setState(() {
        if (bioVal != null) _requireBiometrics = bioVal == 'true';
        if (aiVal != null) _requireAiConfirmation = aiVal == 'true';
        _hasGitHubToken = ghPat != null && ghPat.trim().isNotEmpty;
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

  Future<void> _configureGitHubToken() async {
    final existing = await _storage.read('github_pat') ?? '';
    final controller = TextEditingController(text: existing);
    if (!mounted) return;

    final action = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('GitHub Personal Access Token'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Enter a GitHub PAT (with "repo" scope) to list and clone both public and private repositories. Optional if DEVORBIT_GITHUB_TOKEN is set on your host PC.',
              style: TextStyle(fontSize: 12, color: Colors.grey),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: controller,
              obscureText: true,
              decoration: const InputDecoration(
                labelText: 'GitHub PAT (ghp_... or github_pat_...)',
                border: OutlineInputBorder(),
              ),
            ),
          ],
        ),
        actions: [
          if (_hasGitHubToken)
            TextButton(
              onPressed: () => Navigator.pop(context, 'clear'),
              style: TextButton.styleFrom(foregroundColor: Colors.redAccent),
              child: const Text('Clear'),
            ),
          TextButton(
            onPressed: () => Navigator.pop(context, 'cancel'),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, 'save'),
            child: const Text('Save'),
          ),
        ],
      ),
    );

    if (!mounted) return;
    if (action == 'save') {
      final token = controller.text.trim();
      await _storage.write('github_pat', token);
      if (mounted) {
        setState(() {
          _hasGitHubToken = token.isNotEmpty;
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(token.isNotEmpty
                ? 'GitHub PAT saved in secure storage.'
                : 'GitHub PAT cleared.'),
          ),
        );
      }
    } else if (action == 'clear') {
      await _storage.write('github_pat', '');
      if (mounted) {
        setState(() {
          _hasGitHubToken = false;
        });
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('GitHub PAT cleared from secure storage.')),
        );
      }
    }
    controller.dispose();
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
          leading: const Icon(Icons.code, color: Colors.purpleAccent),
          title: const Text('GitHub Personal Access Token'),
          subtitle: Text(
            _hasGitHubToken
                ? 'Configured (public & private repos enabled)'
                : 'Tap to add GitHub PAT for private & public repos',
          ),
          trailing: Icon(
            _hasGitHubToken ? Icons.check_circle : Icons.chevron_right,
            color: _hasGitHubToken ? Colors.green : Colors.grey,
          ),
          onTap: _configureGitHubToken,
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


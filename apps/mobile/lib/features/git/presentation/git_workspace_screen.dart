import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/direct_e2e_http_client.dart';
import '../../../core/security/secure_storage.dart';

class GitWorkspaceScreen extends StatefulWidget {
  final String? workspaceId;

  const GitWorkspaceScreen({super.key, this.workspaceId});

  @override
  State<GitWorkspaceScreen> createState() => _GitWorkspaceScreenState();
}

class _GitWorkspaceScreenState extends State<GitWorkspaceScreen> with SingleTickerProviderStateMixin {
  late TabController _tabController;

  String _branch = 'main';
  bool _isClean = true;
  List<String> _stagedFiles = [];
  List<String> _unstagedFiles = [];
  List<String> _untrackedFiles = [];
  String _diffText = 'No active diff changes.';
  bool _isLoading = false;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
    _fetchLiveGitData();
  }

  Future<void> _fetchLiveGitData() async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final session = await SecureStorageService.instance.getPairedSession();
    if (session == null || session['brokerUrl'] == null) {
      if (mounted) {
        setState(() {
          _isLoading = false;
        });
      }
      return;
    }

    final client = HostBridgeApiClient(
      baseUrl: DirectE2EHttpTransport.resolveHostBridgeHttpUrl(session['brokerUrl']),
      sessionToken: session['token'],
      sessionId: session['sessionId'],
      deviceId: session['deviceId'],
      masterSecret: session['masterSecret'],
    );

    final wsId = widget.workspaceId ?? (session['machineId'] ?? 'workspace');

    final stRes = await client.getGitStatus(wsId);
    final diffRes = await client.getGitDiff(wsId);

    if (mounted) {
      setState(() {
        _isLoading = false;
        if (stRes != null) {
          _branch = stRes['branch_name'] ?? stRes['branch'] ?? 'main';
          _isClean = stRes['is_clean'] == true;
          _stagedFiles = List<String>.from(stRes['staged_files'] ?? []);
          _unstagedFiles = List<String>.from(stRes['unstaged_files'] ?? []);
          _untrackedFiles = List<String>.from(stRes['untracked_files'] ?? []);
        }

        if (diffRes != null) {
          _diffText = diffRes['diff_output'] ?? diffRes['diff'] ?? 'No uncommitted diffs found on host.';
        }
      });
    }
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // Branch & Status Banner
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 12.0),
          color: const Color(0xFF252526),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Expanded(
                child: Row(
                  children: [
                    const Icon(Icons.alt_route, color: Colors.purpleAccent),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        'Branch: $_branch',
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 14),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Row(
                children: [
                  Chip(
                    label: Text(_isClean ? 'Clean' : 'Modified'),
                    backgroundColor: _isClean
                        ? Colors.green.withValues(alpha: 0.2)
                        : Colors.orange.withValues(alpha: 0.2),
                  ),
                  IconButton(
                    icon: const Icon(Icons.refresh, size: 18),
                    onPressed: _fetchLiveGitData,
                    tooltip: 'Refresh Git Status',
                  ),
                ],
              ),
            ],
          ),
        ),

        // Tabs: Status vs Diff
        TabBar(
          controller: _tabController,
          tabs: const [
            Tab(icon: Icon(Icons.list_alt), text: 'Status Tree'),
            Tab(icon: Icon(Icons.difference), text: 'Diff Inspector'),
          ],
        ),

        if (_errorMessage != null)
          Padding(
            padding: const EdgeInsets.all(8.0),
            child: Text(_errorMessage!, style: const TextStyle(color: Colors.redAccent, fontSize: 12)),
          ),

        Expanded(
          child: _isLoading
              ? const Center(child: CircularProgressIndicator())
              : TabBarView(
                  controller: _tabController,
                  children: [
                    // Status Tree View or Empty State
                    (_stagedFiles.isEmpty && _unstagedFiles.isEmpty && _untrackedFiles.isEmpty)
                        ? Center(
                            child: Padding(
                              padding: const EdgeInsets.all(32.0),
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                children: [
                                  Icon(Icons.check_circle_outline, size: 48, color: Colors.green.shade400),
                                  const SizedBox(height: 12),
                                  const Text(
                                    'Working Tree Clean',
                                    style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                                  ),
                                  const SizedBox(height: 6),
                                  const Text(
                                    'No uncommitted changes, staged files, or untracked modifications in host repository.',
                                    textAlign: TextAlign.center,
                                    style: TextStyle(fontSize: 12, color: Colors.grey),
                                  ),
                                ],
                              ),
                            ),
                          )
                        : ListView(
                            padding: const EdgeInsets.all(16.0),
                            children: [
                              if (_stagedFiles.isNotEmpty) ...[
                                const Text('Staged Changes', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.greenAccent)),
                                const SizedBox(height: 4),
                                ..._stagedFiles.map((f) => ListTile(
                                      dense: true,
                                      contentPadding: EdgeInsets.zero,
                                      leading: const Icon(Icons.check_circle_outline, color: Colors.green, size: 20),
                                      title: Text(f, style: const TextStyle(fontSize: 13)),
                                    )),
                              ],
                              if (_unstagedFiles.isNotEmpty) ...[
                                const SizedBox(height: 12),
                                const Text('Unstaged Changes', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.orangeAccent)),
                                const SizedBox(height: 4),
                                ..._unstagedFiles.map((f) => ListTile(
                                      dense: true,
                                      contentPadding: EdgeInsets.zero,
                                      leading: const Icon(Icons.edit, color: Colors.orange, size: 20),
                                      title: Text(f, style: const TextStyle(fontSize: 13)),
                                    )),
                              ],
                              if (_untrackedFiles.isNotEmpty) ...[
                                const SizedBox(height: 12),
                                const Text('Untracked Files', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.blueAccent)),
                                const SizedBox(height: 4),
                                ..._untrackedFiles.map((f) => ListTile(
                                      dense: true,
                                      contentPadding: EdgeInsets.zero,
                                      leading: const Icon(Icons.add_circle_outline, color: Colors.blue, size: 20),
                                      title: Text(f, style: const TextStyle(fontSize: 13)),
                                    )),
                              ],
                            ],
                          ),

                    // Diff Inspector View
                    Container(
                      color: const Color(0xFF1E1E1E),
                      padding: const EdgeInsets.all(12.0),
                      width: double.infinity,
                      child: SingleChildScrollView(
                        child: SelectableText(
                          _diffText,
                          style: const TextStyle(fontFamily: 'monospace', fontSize: 12.0, color: Colors.white70),
                        ),
                      ),
                    ),
                  ],
                ),
        ),
      ],
    );
  }
}


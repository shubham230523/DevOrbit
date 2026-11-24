import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/security/secure_storage.dart';

class ProjectExplorerScreen extends StatefulWidget {
  final Function(String path)? onFileSelected;
  final Function(String workspacePath)? onOpenInTravelAi;
  final List<Map<String, dynamic>>? initialFiles;
  final bool isActive;

  const ProjectExplorerScreen({
    super.key,
    this.onFileSelected,
    this.onOpenInTravelAi,
    this.initialFiles,
    this.isActive = true,
  });

  @override
  State<ProjectExplorerScreen> createState() => _ProjectExplorerScreenState();
}

class _ProjectExplorerScreenState extends State<ProjectExplorerScreen> {
  final List<String> _breadcrumbs = ['/'];
  String _searchQuery = '';
  List<Map<String, dynamic>> _files = [];
  List<dynamic> _workspaces = [];
  String? _selectedWorkspaceId;
  HostBridgeApiClient? _activeClient;
  bool _isLoading = false;
  String? _errorMessage;

  bool _showGitHubRepos = false;
  List<dynamic> _githubRepos = [];
  bool _isLoadingRepos = false;
  String? _githubError;
  String? _cloningRepoName;

  @override
  void initState() {
    super.initState();
    if (widget.initialFiles != null && widget.initialFiles!.isNotEmpty) {
      _files = widget.initialFiles!;
    } else {
      _fetchHostWorkspaces();
    }
  }

  @override
  void didUpdateWidget(covariant ProjectExplorerScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.isActive &&
        !oldWidget.isActive &&
        widget.initialFiles == null &&
        _workspaces.isEmpty &&
        !_isLoading) {
      _fetchHostWorkspaces();
    }
  }

  Future<void> _fetchHostWorkspaces() async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final session = await SecureStorageService.instance.getPairedSession();
    final token = session?['token'];

    final candidateUrls = <String>[
      'http://localhost:8082',
      'http://127.0.0.1:8082',
      'http://10.0.2.2:8082',
    ];

    if (session != null && session['brokerUrl'] != null && session['brokerUrl']!.isNotEmpty) {
      final bUrl = session['brokerUrl']!
          .replaceAll('ws://', 'http://')
          .replaceAll('wss://', 'https://')
          .replaceAll(':8081', ':8082');
      if (!candidateUrls.contains(bUrl)) {
        candidateUrls.insert(0, bUrl);
      }
    }

    for (final url in candidateUrls) {
      final client = HostBridgeApiClient(baseUrl: url, sessionToken: token);
      final wsRes = await client.listWorkspaces();
      if (wsRes['success'] == true && mounted) {
        final list = wsRes['workspaces'] as List<dynamic>;
        setState(() {
          _activeClient = client;
          _workspaces = list;
          if (list.isNotEmpty) {
            final keepSelected = _selectedWorkspaceId != null &&
                list.any((w) => w['root_path'] == _selectedWorkspaceId);
            if (!keepSelected) {
              _selectedWorkspaceId = list.first['root_path'] as String?;
            }
            _loadDirectoryFiles(client, _selectedWorkspaceId!, '');
          } else {
            _isLoading = false;
          }
        });
        return;
      }
    }

    if (mounted) {
      setState(() {
        _isLoading = false;
        _errorMessage = 'Unable to connect to host workspace service';
      });
    }
  }

  Future<void> _fetchGitHubRepos() async {
    setState(() {
      _isLoadingRepos = true;
      _githubError = null;
    });

    if (_activeClient == null) {
      await _fetchHostWorkspaces();
    }
    final client = _activeClient;
    if (client == null) {
      if (mounted) {
        setState(() {
          _isLoadingRepos = false;
          _githubError = 'Host Bridge offline. Connect on Home tab first.';
        });
      }
      return;
    }

    final pat = await SecureStorageService.instance.read('github_pat');
    final res = await client.listGitHubRepos(githubToken: pat);
    if (!mounted) return;

    if (res['success'] == true) {
      setState(() {
        _githubRepos = res['repositories'] as List<dynamic>;
        _isLoadingRepos = false;
      });
    } else {
      setState(() {
        _isLoadingRepos = false;
        _githubError = res['error']?.toString() ?? 'Failed to load GitHub repositories';
      });
    }
  }

  Future<void> _cloneAndOpenRepo(Map<String, dynamic> repo, {bool openInAi = false}) async {
    final name = repo['name']?.toString() ?? '';
    final cloneUrl = repo['clone_url']?.toString() ?? '';
    if (name.isEmpty || cloneUrl.isEmpty || _activeClient == null) return;

    setState(() {
      _cloningRepoName = name;
    });

    final pat = await SecureStorageService.instance.read('github_pat');
    final res = await _activeClient!.cloneGitHubRepo(
      cloneUrl: cloneUrl,
      name: name,
      githubToken: pat,
    );

    if (!mounted) return;
    setState(() {
      _cloningRepoName = null;
    });

    if (res['success'] == true) {
      final data = res['data'] as Map<String, dynamic>;
      final ws = data['workspace'] as Map<String, dynamic>?;
      final rootPath = ws?['root_path']?.toString();

      await _fetchHostWorkspaces();
      await _fetchGitHubRepos();

      if (rootPath != null && mounted) {
        setState(() {
          _selectedWorkspaceId = rootPath;
          _breadcrumbs
            ..clear()
            ..add('/');
          _showGitHubRepos = false;
        });
        await _loadDirectoryFiles(_activeClient!, rootPath, '');

        if (openInAi && widget.onOpenInTravelAi != null) {
          widget.onOpenInTravelAi!(rootPath);
        }
      }
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('Clone failed: ${res['error']}'),
          backgroundColor: Colors.redAccent,
        ),
      );
    }
  }

  Future<void> _loadDirectoryFiles(HostBridgeApiClient client, String workspaceId, String path) async {
    setState(() {
      _isLoading = true;
    });

    final dirRes = await client.listDirectory(workspaceId, path);
    if (dirRes != null && mounted) {
      final items = dirRes['entries'] as List<dynamic>? ?? [];
      final mapped = items.map<Map<String, dynamic>>((e) {
        final isDir = e['is_directory'] == true;
        final name = e['name'] as String? ?? 'file';
        final ext = name.contains('.') ? name.split('.').last : '';
        return {
          'name': name,
          'path': e['relative_path'] ?? name,
          'is_dir': isDir,
          'size': e['size_bytes'] ?? 0,
          'type': isDir ? 'folder' : ext,
        };
      }).toList();

      setState(() {
        _files = mapped;
        _isLoading = false;
      });
    } else if (mounted) {
      setState(() {
        _isLoading = false;
      });
    }
  }

  IconData _getFileIcon(String type, bool isDir) {
    if (isDir) return Icons.folder;
    switch (type.toLowerCase()) {
      case 'gradle':
      case 'kts':
        return Icons.build;
      case 'markdown':
      case 'md':
        return Icons.description;
      case 'kt':
      case 'dart':
      case 'go':
      case 'java':
        return Icons.code;
      default:
        return Icons.insert_drive_file;
    }
  }

  Color _getFileIconColor(String type, bool isDir) {
    if (isDir) return Colors.amber;
    switch (type.toLowerCase()) {
      case 'gradle':
      case 'kts':
        return Colors.tealAccent;
      case 'markdown':
      case 'md':
        return Colors.lightBlueAccent;
      case 'kt':
      case 'dart':
      case 'go':
      case 'java':
        return Colors.purpleAccent;
      default:
        return Colors.grey;
    }
  }

  @override
  Widget build(BuildContext context) {
    final filteredFiles = _files.where((f) {
      if (_searchQuery.isEmpty) return true;
      return f['name'].toString().toLowerCase().contains(_searchQuery.toLowerCase());
    }).toList();

    final filteredRepos = _githubRepos.where((r) {
      if (_searchQuery.isEmpty) return true;
      final q = _searchQuery.toLowerCase();
      final name = (r['name'] ?? '').toString().toLowerCase();
      final desc = (r['description'] ?? '').toString().toLowerCase();
      return name.contains(q) || desc.contains(q);
    }).toList();

    return Column(
      children: [
        // Top Mode Bar: Host Files vs GitHub Repos
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12.0, vertical: 6.0),
          color: const Color(0xFF181818),
          child: Row(
            children: [
              Expanded(
                child: ChoiceChip(
                  label: const Center(child: Text('Host Files')),
                  selected: !_showGitHubRepos,
                  onSelected: (selected) {
                    if (selected && _showGitHubRepos) {
                      setState(() {
                        _showGitHubRepos = false;
                        _searchQuery = '';
                      });
                    }
                  },
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: ChoiceChip(
                  label: const Center(child: Text('GitHub Repos')),
                  selected: _showGitHubRepos,
                  onSelected: (selected) {
                    if (selected && !_showGitHubRepos) {
                      setState(() {
                        _showGitHubRepos = true;
                        _searchQuery = '';
                      });
                      if (_githubRepos.isEmpty && !_isLoadingRepos) {
                        _fetchGitHubRepos();
                      }
                    }
                  },
                ),
              ),
            ],
          ),
        ),

        if (!_showGitHubRepos) ...[
          // Workspace Selector Header if workspaces available
          if (_workspaces.isNotEmpty)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 6.0),
              color: const Color(0xFF1E1E1E),
              child: Row(
                children: [
                  const Icon(Icons.folder_special, color: Colors.purpleAccent, size: 20),
                  const SizedBox(width: 8),
                  Expanded(
                    child: DropdownButton<String>(
                      value: _selectedWorkspaceId,
                      isExpanded: true,
                      underline: const SizedBox(),
                      items: _workspaces.map<DropdownMenuItem<String>>((ws) {
                        final name = ws['name'] ?? 'Project';
                        final rootPath = ws['root_path'] ?? '';
                        return DropdownMenuItem<String>(
                          value: rootPath.toString(),
                          child: Text('$name ($rootPath)', overflow: TextOverflow.ellipsis),
                        );
                      }).toList(),
                      onChanged: (val) {
                        if (val != null) {
                          setState(() {
                            _selectedWorkspaceId = val;
                            _breadcrumbs.clear();
                            _breadcrumbs.add('/');
                          });
                          _fetchHostWorkspaces();
                        }
                      },
                    ),
                  ),
                  if (_selectedWorkspaceId != null && widget.onOpenInTravelAi != null)
                    IconButton(
                      icon: const Icon(Icons.auto_awesome, color: Colors.purpleAccent, size: 20),
                      tooltip: 'Code in this project with Travel AI',
                      onPressed: () => widget.onOpenInTravelAi!(_selectedWorkspaceId!),
                    ),
                ],
              ),
            ),

          // Breadcrumb Navigation Bar
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
            color: const Color(0xFF252526),
            child: Row(
              children: [
                const Icon(Icons.location_on, size: 16, color: Colors.grey),
                const SizedBox(width: 8),
                Expanded(
                  child: SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Row(
                      children: _breadcrumbs.map((crumb) {
                        return Padding(
                          padding: const EdgeInsets.only(right: 4.0),
                          child: Chip(
                            labelStyle: const TextStyle(fontSize: 11),
                            padding: EdgeInsets.zero,
                            label: Text(crumb),
                            backgroundColor: Colors.purple.withValues(alpha: 0.2),
                          ),
                        );
                      }).toList(),
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.refresh, size: 18),
                  onPressed: _fetchHostWorkspaces,
                  tooltip: 'Refresh Workspace Listing',
                ),
              ],
            ),
          ),

          // File Search Filter
          Padding(
            padding: const EdgeInsets.all(8.0),
            child: TextField(
              decoration: const InputDecoration(
                hintText: 'Search files or folders...',
                prefixIcon: Icon(Icons.search, size: 20),
                isDense: true,
                border: OutlineInputBorder(),
              ),
              onChanged: (val) {
                setState(() {
                  _searchQuery = val;
                });
              },
            ),
          ),

          if (_errorMessage != null)
            Padding(
              padding: const EdgeInsets.all(8.0),
              child: Text(_errorMessage!, style: const TextStyle(color: Colors.redAccent, fontSize: 12)),
            ),

          // Directory & File List or Empty State
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : filteredFiles.isEmpty
                    ? Center(
                        child: Padding(
                          padding: const EdgeInsets.all(32.0),
                          child: Column(
                            mainAxisAlignment: MainAxisAlignment.center,
                            children: [
                              Icon(Icons.folder_off, size: 48, color: Colors.grey.shade600),
                              const SizedBox(height: 12),
                              const Text(
                                'No workspace files loaded.',
                                style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                              ),
                              const SizedBox(height: 6),
                              const Text(
                                'Connect to an active Host Bridge session or pair a host machine to inspect live project files.',
                                textAlign: TextAlign.center,
                                style: TextStyle(fontSize: 12, color: Colors.grey),
                              ),
                            ],
                          ),
                        ),
                      )
                    : ListView.builder(
                        itemCount: filteredFiles.length,
                        itemBuilder: (context, index) {
                          final file = filteredFiles[index];
                          final isDir = file['is_dir'] == true;
                          final type = (file['type'] ?? 'file') as String;

                          return ListTile(
                            leading: Icon(
                              _getFileIcon(type, isDir),
                              color: _getFileIconColor(type, isDir),
                            ),
                            title: Text(file['name'] as String),
                            subtitle: isDir
                                ? const Text('Directory')
                                : Text('${file['size'] ?? 0} bytes'),
                            trailing: isDir
                                ? const Icon(Icons.chevron_right)
                                : const Icon(Icons.edit_note, color: Colors.grey),
                            onTap: () {
                              if (isDir) {
                                setState(() {
                                  _breadcrumbs.add(file['name'] as String);
                                });
                              } else {
                                if (widget.onFileSelected != null) {
                                  widget.onFileSelected!(file['path'] as String);
                                }
                              }
                            },
                          );
                        },
                      ),
          ),
        ] else ...[
          // GitHub Search & Refresh Bar
          Padding(
            padding: const EdgeInsets.all(8.0),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    decoration: const InputDecoration(
                      hintText: 'Search GitHub repositories...',
                      prefixIcon: Icon(Icons.search, size: 20),
                      isDense: true,
                      border: OutlineInputBorder(),
                    ),
                    onChanged: (val) {
                      setState(() {
                        _searchQuery = val;
                      });
                    },
                  ),
                ),
                const SizedBox(width: 8),
                IconButton(
                  icon: const Icon(Icons.refresh),
                  onPressed: _isLoadingRepos ? null : _fetchGitHubRepos,
                  tooltip: 'Refresh GitHub Repositories',
                ),
              ],
            ),
          ),

          if (_githubError != null)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12.0, vertical: 6.0),
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  color: Colors.red.shade900.withValues(alpha: 0.25),
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: Colors.redAccent),
                ),
                child: Text(
                  _githubError!,
                  style: const TextStyle(color: Colors.redAccent, fontSize: 12),
                ),
              ),
            ),

          Expanded(
            child: _isLoadingRepos
                ? const Center(child: CircularProgressIndicator())
                : filteredRepos.isEmpty
                    ? const Center(
                        child: Padding(
                          padding: EdgeInsets.all(24.0),
                          child: Text(
                            'No repositories loaded.\nAdd your GitHub PAT in Settings -> GitHub Personal Access Token (or set DEVORBIT_GITHUB_TOKEN on the host) and tap Refresh.',
                            textAlign: TextAlign.center,
                            style: TextStyle(color: Colors.grey, fontSize: 13),
                          ),
                        ),
                      )
                    : ListView.builder(
                        itemCount: filteredRepos.length,
                        itemBuilder: (context, index) {
                          final repo = filteredRepos[index] as Map<String, dynamic>;
                          final name = repo['name']?.toString() ?? '';
                          final isPrivate = repo['private'] == true;
                          final lang = repo['language']?.toString() ?? '';
                          final desc = repo['description']?.toString() ?? '';
                          final isCloned = repo['is_cloned'] == true;
                          final localPath = repo['local_path']?.toString();
                          final isCloning = _cloningRepoName == name;

                          return Card(
                            margin: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                            child: ListTile(
                              leading: Icon(
                                isPrivate ? Icons.lock : Icons.public,
                                color: isPrivate ? Colors.amberAccent : Colors.purpleAccent,
                              ),
                              title: Row(
                                children: [
                                  Expanded(
                                    child: Text(
                                      name,
                                      overflow: TextOverflow.ellipsis,
                                      style: const TextStyle(fontWeight: FontWeight.w600),
                                    ),
                                  ),
                                  Container(
                                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                                    decoration: BoxDecoration(
                                      color: (isPrivate ? Colors.amber : Colors.purple)
                                          .withValues(alpha: 0.2),
                                      borderRadius: BorderRadius.circular(4),
                                    ),
                                    child: Text(
                                      isPrivate ? 'Private' : 'Public',
                                      style: TextStyle(
                                        fontSize: 10,
                                        color: isPrivate ? Colors.amberAccent : Colors.purpleAccent,
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                              subtitle: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  if (desc.isNotEmpty)
                                    Text(desc, maxLines: 2, overflow: TextOverflow.ellipsis),
                                  if (lang.isNotEmpty)
                                    Padding(
                                      padding: const EdgeInsets.only(top: 4),
                                      child: Text(lang,
                                          style: const TextStyle(fontSize: 11, color: Colors.grey)),
                                    ),
                                ],
                              ),
                              trailing: isCloning
                                  ? const SizedBox(
                                      width: 22,
                                      height: 22,
                                      child: CircularProgressIndicator(strokeWidth: 2),
                                    )
                                  : isCloned && localPath != null
                                      ? Row(
                                          mainAxisSize: MainAxisSize.min,
                                          children: [
                                            IconButton(
                                              icon: const Icon(Icons.folder_open, size: 20),
                                              tooltip: 'Open Files',
                                              onPressed: () {
                                                setState(() {
                                                  _selectedWorkspaceId = localPath;
                                                  _breadcrumbs
                                                    ..clear()
                                                    ..add('/');
                                                  _showGitHubRepos = false;
                                                });
                                                if (_activeClient != null) {
                                                  _loadDirectoryFiles(_activeClient!, localPath, '');
                                                }
                                              },
                                            ),
                                            if (widget.onOpenInTravelAi != null)
                                              IconButton(
                                                icon: const Icon(Icons.auto_awesome,
                                                    color: Colors.purpleAccent, size: 20),
                                                tooltip: 'Code with Travel AI',
                                                onPressed: () =>
                                                    widget.onOpenInTravelAi!(localPath),
                                              ),
                                          ],
                                        )
                                      : ElevatedButton(
                                          onPressed: () =>
                                              _cloneAndOpenRepo(repo, openInAi: true),
                                          child: const Text('Clone & AI'),
                                        ),
                            ),
                          );
                        },
                      ),
          ),
        ],
      ],
    );
  }
}



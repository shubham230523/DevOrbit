import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/security/secure_storage.dart';

class ProjectExplorerScreen extends StatefulWidget {
  final Function(String path)? onFileSelected;
  final List<Map<String, dynamic>>? initialFiles;

  const ProjectExplorerScreen({
    super.key,
    this.onFileSelected,
    this.initialFiles,
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
  bool _isLoading = false;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    if (widget.initialFiles != null && widget.initialFiles!.isNotEmpty) {
      _files = widget.initialFiles!;
    } else {
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
          _workspaces = list;
          if (list.isNotEmpty) {
            _selectedWorkspaceId = list.first['root_path'] as String?;
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

    return Column(
      children: [
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
      ],
    );
  }
}



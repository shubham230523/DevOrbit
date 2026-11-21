import 'package:flutter/material.dart';

class ProjectExplorerScreen extends StatefulWidget {
  final Function(String path)? onFileSelected;

  const ProjectExplorerScreen({super.key, this.onFileSelected});

  @override
  State<ProjectExplorerScreen> createState() => _ProjectExplorerScreenState();
}

class _ProjectExplorerScreenState extends State<ProjectExplorerScreen> {
  final List<String> _breadcrumbs = ['/'];
  String _searchQuery = '';

  final List<Map<String, dynamic>> _files = [
    {
      'name': 'app',
      'path': 'app',
      'is_dir': true,
      'size': 0,
      'type': 'folder',
    },
    {
      'name': 'build.gradle.kts',
      'path': 'build.gradle.kts',
      'is_dir': false,
      'size': 1420,
      'type': 'gradle',
    },
    {
      'name': 'settings.gradle.kts',
      'path': 'settings.gradle.kts',
      'is_dir': false,
      'size': 680,
      'type': 'gradle',
    },
    {
      'name': 'README.md',
      'path': 'README.md',
      'is_dir': false,
      'size': 2040,
      'type': 'markdown',
    },
  ];

  IconData _getFileIcon(String type, bool isDir) {
    if (isDir) return Icons.folder;
    switch (type) {
      case 'gradle':
        return Icons.build;
      case 'markdown':
        return Icons.description;
      case 'kt':
        return Icons.code;
      default:
        return Icons.insert_drive_file;
    }
  }

  Color _getFileIconColor(String type, bool isDir) {
    if (isDir) return Colors.amber;
    switch (type) {
      case 'gradle':
        return Colors.tealAccent;
      case 'markdown':
        return Colors.lightBlueAccent;
      case 'kt':
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
                onPressed: () {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('Refreshed directory listing from remote host')),
                  );
                },
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

        // Directory & File List
        Expanded(
          child: ListView.builder(
            itemCount: filteredFiles.length,
            itemBuilder: (context, index) {
              final file = filteredFiles[index];
              final isDir = file['is_dir'] as bool;
              final type = file['type'] as String;

              return ListTile(
                leading: Icon(
                  _getFileIcon(type, isDir),
                  color: _getFileIconColor(type, isDir),
                ),
                title: Text(file['name'] as String),
                subtitle: isDir
                    ? const Text('Directory')
                    : Text('${file['size']} bytes'),
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

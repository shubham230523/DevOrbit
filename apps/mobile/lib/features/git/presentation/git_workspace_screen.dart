import 'package:flutter/material.dart';

class GitWorkspaceScreen extends StatefulWidget {
  const GitWorkspaceScreen({super.key});

  @override
  State<GitWorkspaceScreen> createState() => _GitWorkspaceScreenState();
}

class _GitWorkspaceScreenState extends State<GitWorkspaceScreen> with SingleTickerProviderStateMixin {
  late TabController _tabController;

  final String _branch = 'main';
  final bool _isClean = false;
  final List<String> _stagedFiles = ['apps/mobile/pubspec.yaml'];
  final List<String> _unstagedFiles = ['apps/mobile/lib/main.dart'];
  final List<String> _untrackedFiles = ['docs/planning/STAGE_3_IMPLEMENTATION_PLAN.md'];

  final String _diffText = '''diff --git a/apps/mobile/lib/main.dart b/apps/mobile/lib/main.dart
index a1b2c3d..e4f5g6h 100644
--- a/apps/mobile/lib/main.dart
+++ b/apps/mobile/lib/main.dart
@@ -10,6 +10,7 @@ void main() {
+  // Added Stage 3 Mobile IDE Workspace Integration
+  runApp(const DevOrbitApp());
 }''';

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
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
          padding: const EdgeInsets.all(16.0),
          color: const Color(0xFF252526),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Row(
                children: [
                  const Icon(Icons.alt_route, color: Colors.purpleAccent),
                  const SizedBox(width: 8),
                  Text('Branch: $_branch', style: const TextStyle(fontWeight: FontWeight.bold)),
                ],
              ),
              Chip(
                label: Text(_isClean ? 'Clean' : 'Modified'),
                backgroundColor: _isClean
                    ? Colors.green.withValues(alpha: 0.2)
                    : Colors.orange.withValues(alpha: 0.2),
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

        Expanded(
          child: TabBarView(
            controller: _tabController,
            children: [
              // Status Tree View
              ListView(
                padding: const EdgeInsets.all(16.0),
                children: [
                  if (_stagedFiles.isNotEmpty) ...[
                    const Text('Staged Changes', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.greenAccent)),
                    ..._stagedFiles.map((f) => ListTile(
                          leading: const Icon(Icons.check_circle_outline, color: Colors.green),
                          title: Text(f),
                        )),
                  ],
                  if (_unstagedFiles.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    const Text('Unstaged Changes', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.orangeAccent)),
                    ..._unstagedFiles.map((f) => ListTile(
                          leading: const Icon(Icons.edit, color: Colors.orange),
                          title: Text(f),
                        )),
                  ],
                  if (_untrackedFiles.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    const Text('Untracked Files', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.blueAccent)),
                    ..._untrackedFiles.map((f) => ListTile(
                          leading: const Icon(Icons.add_circle_outline, color: Colors.blue),
                          title: Text(f),
                        )),
                  ],
                ],
              ),

              // Diff Inspector View
              Container(
                color: const Color(0xFF1E1E1E),
                padding: const EdgeInsets.all(12.0),
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

import 'package:flutter/material.dart';

class RemoteTasksScreen extends StatefulWidget {
  const RemoteTasksScreen({super.key});

  @override
  State<RemoteTasksScreen> createState() => _RemoteTasksScreenState();
}

class _RemoteTasksScreenState extends State<RemoteTasksScreen> {
  final List<String> _logs = [];
  bool _isRunning = false;
  bool _autoScroll = true;
  final ScrollController _scrollController = ScrollController();

  void _runTask(String taskName, String command, List<String> args) async {
    setState(() {
      _isRunning = true;
      _logs.add('--- Executing Task: $taskName ($command ${args.join(" ")}) ---');
    });

    _scrollToBottom();

    // Stream simulated output chunks from host
    final chunks = [
      'Initializing Gradle Daemon...',
      'Executing tasks: [:app:assembleDebug]',
      '> Task :app:preBuild UP-TO-DATE',
      '> Task :app:compileDebugKotlin OK',
      '> Task :app:compileDebugJavaWithJavac OK',
      'BUILD SUCCESSFUL in 3s (5 actionable tasks: 3 executed, 2 up-to-date)',
    ];

    for (final chunk in chunks) {
      await Future.delayed(const Duration(milliseconds: 500));
      if (!mounted) return;
      setState(() {
        _logs.add(chunk);
      });
      _scrollToBottom();
    }

    if (mounted) {
      setState(() {
        _isRunning = false;
        _logs.add('--- Task Completed with exit code 0 ---\n');
      });
      _scrollToBottom();
    }
  }

  void _scrollToBottom() {
    if (_autoScroll && _scrollController.hasClients) {
      _scrollController.animateTo(
        _scrollController.position.maxScrollExtent,
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
      );
    }
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // Quick Action Bar
        Padding(
          padding: const EdgeInsets.all(8.0),
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                ElevatedButton.icon(
                  onPressed: _isRunning ? null : () => _runTask('Gradle Build', 'gradlew', ['assembleDebug']),
                  icon: const Icon(Icons.play_arrow, size: 16),
                  label: const Text('Gradle Build'),
                ),
                const SizedBox(width: 8),
                ElevatedButton.icon(
                  onPressed: _isRunning ? null : () => _runTask('Run Tests', 'gradlew', ['test']),
                  icon: const Icon(Icons.bug_report, size: 16),
                  label: const Text('Gradle Test'),
                ),
                const SizedBox(width: 8),
                ElevatedButton.icon(
                  onPressed: _isRunning ? null : () => _runTask('ADB Devices', 'adb', ['devices', '-l']),
                  icon: const Icon(Icons.phone_android, size: 16),
                  label: const Text('ADB Devices'),
                ),
                const SizedBox(width: 8),
                if (_isRunning)
                  OutlinedButton.icon(
                    onPressed: () {
                      setState(() {
                        _isRunning = false;
                        _logs.add('--- Process Cancelled by User ---');
                      });
                    },
                    icon: const Icon(Icons.stop, color: Colors.redAccent),
                    label: const Text('Cancel Process', style: TextStyle(color: Colors.redAccent)),
                  ),
              ],
            ),
          ),
        ),

        // Terminal Output Header
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
          color: const Color(0xFF252526),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              const Row(
                children: [
                  Icon(Icons.terminal, size: 16, color: Colors.tealAccent),
                  SizedBox(width: 8),
                  Text('Host Build Output Terminal', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12)),
                ],
              ),
              Row(
                children: [
                  IconButton(
                    icon: Icon(_autoScroll ? Icons.south : Icons.pause_circle_outline, size: 18),
                    tooltip: _autoScroll ? 'Auto-scroll enabled' : 'Auto-scroll paused',
                    onPressed: () {
                      setState(() {
                        _autoScroll = !_autoScroll;
                      });
                    },
                  ),
                  IconButton(
                    icon: const Icon(Icons.clear_all, size: 18),
                    tooltip: 'Clear Terminal',
                    onPressed: () {
                      setState(() {
                        _logs.clear();
                      });
                    },
                  ),
                ],
              ),
            ],
          ),
        ),

        // Streaming Terminal Log View
        Expanded(
          child: Container(
            color: const Color(0xFF121212),
            padding: const EdgeInsets.all(12.0),
            child: ListView.builder(
              controller: _scrollController,
              itemCount: _logs.length,
              itemBuilder: (context, index) {
                final logLine = _logs[index];
                final isHeader = logLine.startsWith('---');
                final isSuccess = logLine.contains('SUCCESSFUL');
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: 2.0),
                  child: Text(
                    logLine,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12.0,
                      color: isHeader
                          ? Colors.purpleAccent
                          : isSuccess
                              ? Colors.greenAccent
                              : Colors.white70,
                    ),
                  ),
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}

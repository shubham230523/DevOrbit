import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/direct_e2e_http_client.dart';
import '../../../core/security/secure_storage.dart';

class RemoteTasksScreen extends StatefulWidget {
  final String? workspaceId;

  const RemoteTasksScreen({super.key, this.workspaceId});

  @override
  State<RemoteTasksScreen> createState() => _RemoteTasksScreenState();
}

class _RemoteTasksScreenState extends State<RemoteTasksScreen> {
  final List<String> _logs = [];
  bool _isRunning = false;
  bool _autoScroll = true;
  final ScrollController _scrollController = ScrollController();

  Future<void> _runTask(String taskName, String command, List<String> args) async {
    setState(() {
      _isRunning = true;
      _logs.add('--- Executing Task: $taskName ($command ${args.join(" ")}) ---');
    });

    _scrollToBottom();

    final session = await SecureStorageService.instance.getPairedSession();
    if (session == null || session['brokerUrl'] == null) {
      if (mounted) {
        setState(() {
          _isRunning = false;
          _logs.add('--- Error: No active host session. Pair machine to execute terminal commands. ---');
        });
        _scrollToBottom();
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

    final res = await client.executeProcess(
      workspaceId: wsId,
      command: command,
      args: args,
    );

    if (mounted) {
      setState(() {
        _isRunning = false;
      });

      if (res != null) {
        final stdout = res['stdout'] as String? ?? '';
        final stderr = res['stderr'] as String? ?? '';
        final exitCode = res['exit_code'] ?? 0;

        if (stdout.isNotEmpty) {
          _logs.add(stdout);
        }
        if (stderr.isNotEmpty) {
          _logs.add('[STDERR] $stderr');
        }

        _logs.add('--- Task Completed with exit code $exitCode ---\n');
      } else {
        _logs.add('--- Task Execution Error: Host bridge did not return a valid response ---');
      }

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
                  style: ElevatedButton.styleFrom(backgroundColor: Colors.purpleAccent, foregroundColor: Colors.white),
                ),
                const SizedBox(width: 8),
                ElevatedButton.icon(
                  onPressed: _isRunning ? null : () => _runTask('Run Tests', 'gradlew', ['test']),
                  icon: const Icon(Icons.bug_report, size: 16),
                  label: const Text('Gradle Test'),
                  style: ElevatedButton.styleFrom(backgroundColor: Colors.deepPurple, foregroundColor: Colors.white),
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
                        _logs.add('--- Process Execution Stopped ---');
                      });
                    },
                    icon: const Icon(Icons.stop, color: Colors.redAccent),
                    label: const Text('Stop', style: TextStyle(color: Colors.redAccent)),
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
                  Text('Host Terminal Output', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12)),
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

        // Streaming Terminal Log View or Empty Terminal Prompt
        Expanded(
          child: Container(
            color: const Color(0xFF121212),
            padding: const EdgeInsets.all(12.0),
            width: double.infinity,
            child: _logs.isEmpty
                ? const Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    crossAxisAlignment: CrossAxisAlignment.center,
                    children: [
                      Icon(Icons.terminal, size: 48, color: Colors.grey),
                      SizedBox(height: 12),
                      Text(
                        'Host Terminal Ready',
                        style: TextStyle(fontWeight: FontWeight.bold, color: Colors.tealAccent, fontSize: 15),
                      ),
                      SizedBox(height: 6),
                      Text(
                        'Select a quick task above (Gradle Build, Gradle Test, ADB Devices) to execute commands on your desktop host.',
                        textAlign: TextAlign.center,
                        style: TextStyle(color: Colors.grey, fontSize: 12),
                      ),
                    ],
                  )
                : ListView.builder(
                    controller: _scrollController,
                    itemCount: _logs.length,
                    itemBuilder: (context, index) {
                      final logLine = _logs[index];
                      final isHeader = logLine.startsWith('---');
                      final isSuccess = logLine.contains('SUCCESSFUL') || logLine.contains('Completed');
                      final isErr = logLine.contains('STDERR') || logLine.contains('Error');
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
                                    : isErr
                                        ? Colors.redAccent
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


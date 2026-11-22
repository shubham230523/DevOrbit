import 'dart:async';
import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/agent_api_client.dart';
import '../../../core/security/secure_storage.dart';
import 'widgets/approval_dialog.dart';

class TravelCodingScreen extends StatefulWidget {
  final String hostBaseUrl;
  final String? sessionToken;

  const TravelCodingScreen({
    super.key,
    this.hostBaseUrl = 'http://10.0.2.2:8082',
    this.sessionToken,
  });

  @override
  State<TravelCodingScreen> createState() => _TravelCodingScreenState();
}

class _TravelCodingScreenState extends State<TravelCodingScreen> {
  late HostBridgeApiClient _hostClient;
  late AgentApiClient _agentClient;
  late SecureStorageService _secureStorage;

  List<dynamic> _workspaces = [];
  String? _selectedWorkspaceId;
  String _selectedModel = 'gemini-2.5-flash';
  final TextEditingController _promptController = TextEditingController();

  Map<String, dynamic>? _activeTask;
  Timer? _pollingTimer;
  bool _isSubmitting = false;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    _hostClient = HostBridgeApiClient(baseUrl: widget.hostBaseUrl, sessionToken: widget.sessionToken);
    _agentClient = AgentApiClient(baseUrl: widget.hostBaseUrl, sessionToken: widget.sessionToken);
    _secureStorage = SecureStorageService.instance;
    _initHostProjects();
    _restoreActiveTask();
  }

  Future<void> _initHostProjects() async {
    final wsRes = await _hostClient.listWorkspaces();
    if (wsRes['success'] == true && mounted) {
      final list = wsRes['workspaces'] as List<dynamic>;
      setState(() {
        _workspaces = list;
        if (list.isNotEmpty) {
          _selectedWorkspaceId = list.first['root_path'];
        }
      });
    }
  }

  Future<void> _restoreActiveTask() async {
    final savedTaskId = await _secureStorage.read('last_active_task_id');
    if (savedTaskId != null && savedTaskId.isNotEmpty) {
      _pollTaskStatus(savedTaskId);
    }
  }

  void _submitCodingRequest() async {
    final prompt = _promptController.text.trim();
    if (prompt.isEmpty || _selectedWorkspaceId == null) return;

    setState(() {
      _isSubmitting = true;
      _errorMessage = null;
    });

    final res = await _agentClient.createAgentTask(
      workspaceId: _selectedWorkspaceId!,
      prompt: prompt,
      modelId: _selectedModel,
    );

    if (res['success'] == true && mounted) {
      final task = res['task'] as Map<String, dynamic>;
      final taskId = task['id'] as String;
      await _secureStorage.write('last_active_task_id', taskId);

      _promptController.clear();
      setState(() {
        _activeTask = task;
        _isSubmitting = false;
      });

      _startTaskPolling(taskId);
    } else if (mounted) {
      setState(() {
        _isSubmitting = false;
        _errorMessage = res['error'] ?? 'Failed to submit task to Host Bridge';
      });
    }
  }

  void _startTaskPolling(String taskId) {
    _pollingTimer?.cancel();
    _pollingTimer = Timer.periodic(const Duration(milliseconds: 1500), (_) => _pollTaskStatus(taskId));
  }

  void _pollTaskStatus(String taskId) async {
    final res = await _agentClient.getAgentTask(taskId);
    if (res['success'] == true && mounted) {
      final task = res['task'] as Map<String, dynamic>;
      final status = task['status'];

      setState(() {
        _activeTask = task;
      });

      if (status == 'completed' || status == 'failed' || status == 'cancelled') {
        _pollingTimer?.cancel();
        await _secureStorage.delete('last_active_task_id');
      }
    }
  }

  void _approveTaskProposal() async {
    if (_activeTask == null) return;

    final taskId = _activeTask!['id'] as String;
    final token = _activeTask!['approval_token'] as String?;

    if (token == null) return;

    final events = _activeTask!['events'] as List<dynamic>? ?? [];
    Map<String, dynamic>? proposalData;
    for (final evt in events.reversed) {
      if (evt['type'] == 'approval_required' && evt['data']?['proposal'] != null) {
        proposalData = evt['data']['proposal'] as Map<String, dynamic>;
        break;
      }
    }

    if (proposalData == null) return;

    final approved = await showDialog<bool>(
      context: context,
      builder: (context) => TravelApprovalDialog(
        taskId: taskId,
        toolName: proposalData!['tool_name'] ?? 'edit_file',
        targetPath: proposalData['target_path'],
        expectedSHA256: proposalData['expected_sha256'],
        targetContent: proposalData['target_content'],
        replacement: proposalData['replacement'],
        command: proposalData['command'],
        args: proposalData['args'],
        reason: proposalData['reason'] ?? 'Approval Required',
      ),
    );

    if (approved == true && mounted) {
      final approveRes = await _agentClient.approveAgentTask(taskId: taskId, approvalToken: token);
      if (mounted) {
        if (approveRes['success'] == true) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text('Operation approved! Host Bridge resuming task execution...'),
              backgroundColor: Colors.green,
            ),
          );
          _startTaskPolling(taskId);
        } else {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text('Approval submission error: ${approveRes["error"]}'),
              backgroundColor: Colors.red,
            ),
          );
        }
      }
    }
  }

  void _cancelActiveTask() async {
    if (_activeTask == null) return;

    final taskId = _activeTask!['id'] as String;
    final res = await _agentClient.cancelAgentTask(taskId);
    if (res['success'] == true && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Task cancellation sent to Host Bridge.')),
      );
      _pollTaskStatus(taskId);
    }
  }

  @override
  void dispose() {
    _pollingTimer?.cancel();
    _promptController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final taskStatus = _activeTask?['status'] ?? 'none';
    final isRunning = taskStatus == 'running';
    final needsApproval = taskStatus == 'approval_required';

    return Scaffold(
      appBar: AppBar(
        title: const Text('Travel AI Coding Cockpit'),
        backgroundColor: const Color(0xFF1E1E1E),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _initHostProjects,
            tooltip: 'Refresh Host Workspaces',
          ),
        ],
      ),
      body: Column(
        children: [
          // Project and Model Selector Bar
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
            color: const Color(0xFF252526),
            child: Row(
              children: [
                const Icon(Icons.folder_open, color: Colors.amber, size: 20),
                const SizedBox(width: 8),
                Expanded(
                  child: _workspaces.isEmpty
                      ? const Text('Loading host workspaces...', style: TextStyle(color: Colors.grey))
                      : DropdownButton<String>(
                          value: _selectedWorkspaceId,
                          isExpanded: true,
                          underline: const SizedBox(),
                          items: _workspaces.map<DropdownMenuItem<String>>((ws) {
                            final name = ws['name'] ?? 'Project';
                            final path = ws['root_path'] ?? '';
                            return DropdownMenuItem<String>(
                              value: path,
                              child: Text('$name ($path)', overflow: TextOverflow.ellipsis),
                            );
                          }).toList(),
                          onChanged: (val) {
                            if (val != null) {
                              setState(() {
                                _selectedWorkspaceId = val;
                              });
                            }
                          },
                        ),
                ),
                const SizedBox(width: 12),
                DropdownButton<String>(
                  value: _selectedModel,
                  underline: const SizedBox(),
                  items: const [
                    DropdownMenuItem(value: 'gemini-2.5-flash', child: Text('Gemini 2.5 Flash')),
                    DropdownMenuItem(value: 'gemini-2.5-pro', child: Text('Gemini 2.5 Pro')),
                    DropdownMenuItem(value: 'antigravity-gemini-3.8-flash', child: Text('Gemini 3.8 Flash')),
                  ],
                  onChanged: (val) {
                    if (val != null) {
                      setState(() {
                        _selectedModel = val;
                      });
                    }
                  },
                ),
              ],
            ),
          ),

          if (_errorMessage != null)
            Container(
              padding: const EdgeInsets.all(8.0),
              color: Colors.red[900],
              child: Text(_errorMessage!, style: const TextStyle(color: Colors.white, fontSize: 12)),
            ),

          // Main Task Progress and Activity Stream
          Expanded(
            child: ListView(
              padding: const EdgeInsets.all(16.0),
              children: [
                if (_activeTask != null) ...[
                  // Task Status Card
                  Card(
                    color: const Color(0xFF252526),
                    child: Padding(
                      padding: const EdgeInsets.all(12.0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              Text(
                                'Task: ${_activeTask!["id"]}',
                                style: const TextStyle(fontWeight: FontWeight.bold, color: Colors.white),
                              ),
                              Chip(
                                label: Text(taskStatus.toUpperCase()),
                                backgroundColor: needsApproval
                                    ? Colors.amber[800]
                                    : isRunning
                                        ? Colors.blue[800]
                                        : Colors.green[800],
                              ),
                            ],
                          ),
                          const SizedBox(height: 6),
                          Text('Prompt: ${_activeTask!["prompt"]}', style: const TextStyle(color: Colors.grey)),
                          if (isRunning) ...[
                            const SizedBox(height: 10),
                            const Row(
                              children: [
                                SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2)),
                                SizedBox(width: 8),
                                Text('Gemini executing reasoning loop on host...', style: TextStyle(color: Colors.amber, fontSize: 12)),
                              ],
                            ),
                          ],
                          if (needsApproval) ...[
                            const SizedBox(height: 10),
                            ElevatedButton.icon(
                              onPressed: _approveTaskProposal,
                              icon: const Icon(Icons.gavel),
                              label: const Text('Review & Approve Changes'),
                              style: ElevatedButton.styleFrom(backgroundColor: Colors.amber[800]),
                            ),
                          ],
                          if (isRunning || needsApproval) ...[
                            const SizedBox(height: 6),
                            TextButton.icon(
                              onPressed: _cancelActiveTask,
                              icon: const Icon(Icons.cancel, size: 16, color: Colors.redAccent),
                              label: const Text('Cancel Task', style: TextStyle(color: Colors.redAccent)),
                            ),
                          ],
                        ],
                      ),
                    ),
                  ),
                  const SizedBox(height: 12),

                  // Task Activity Log Stream
                  Card(
                    color: const Color(0xFF1E1E1E),
                    child: Padding(
                      padding: const EdgeInsets.all(12.0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text('Host Execution Event Stream', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.white)),
                          const Divider(),
                          ...((_activeTask!['events'] as List<dynamic>? ?? []).map((evt) {
                            final type = evt['type'] ?? '';
                            final msg = evt['message'] ?? '';
                            return Padding(
                              padding: const EdgeInsets.symmetric(vertical: 4.0),
                              child: Row(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  const Icon(Icons.chevron_right, size: 14, color: Colors.tealAccent),
                                  const SizedBox(width: 6),
                                  Expanded(
                                    child: Text(
                                      '[$type] $msg',
                                      style: const TextStyle(fontFamily: 'monospace', fontSize: 11, color: Colors.grey),
                                    ),
                                  ),
                                ],
                              ),
                            );
                          })),
                        ],
                      ),
                    ),
                  ),

                  if (_activeTask!['result_text'] != null && _activeTask!['result_text'].toString().isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Card(
                      color: const Color(0xFF252526),
                      child: Padding(
                        padding: const EdgeInsets.all(12.0),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            const Text('Gemini Final Response', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.greenAccent)),
                            const Divider(),
                            Text(_activeTask!['result_text'], style: const TextStyle(color: Colors.white)),
                          ],
                        ),
                      ),
                    ),
                  ],
                ] else ...[
                  const Center(
                    child: Padding(
                      padding: EdgeInsets.all(32.0),
                      child: Text(
                        'No active coding task.\nSelect a host project and enter a prompt to begin.',
                        textAlign: TextAlign.center,
                        style: TextStyle(color: Colors.grey),
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),

          // Prompt Input Field Bar
          Padding(
            padding: const EdgeInsets.all(12.0),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _promptController,
                    decoration: const InputDecoration(
                      hintText: 'Enter AI coding instructions for Gemini...',
                      border: OutlineInputBorder(),
                      isDense: true,
                    ),
                    onSubmitted: (_) => _submitCodingRequest(),
                  ),
                ),
                const SizedBox(width: 8),
                IconButton(
                  icon: _isSubmitting
                      ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                      : const Icon(Icons.send, color: Colors.amber),
                  onPressed: _isSubmitting ? null : _submitCodingRequest,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

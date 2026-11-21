import 'package:flutter/material.dart';
import 'ai_tool_approval_dialog.dart';

class AiWorkspaceScreen extends StatefulWidget {
  const AiWorkspaceScreen({super.key});

  @override
  State<AiWorkspaceScreen> createState() => _AiWorkspaceScreenState();
}

class _AiWorkspaceScreenState extends State<AiWorkspaceScreen> {
  String _selectedModel = 'Gemini 3.8 Flash (Antigravity)';
  final TextEditingController _promptController = TextEditingController();
  final List<Map<String, String>> _messages = [
    {
      'role': 'assistant',
      'content': 'DevOrbit AI Workspace ready. Connected to Antigravity ACP Agent. How can I assist with your remote Android Studio project?',
    },
  ];

  bool _isStreaming = false;

  void _sendPrompt() async {
    final prompt = _promptController.text.trim();
    if (prompt.isEmpty) return;

    setState(() {
      _messages.add({'role': 'user', 'content': prompt});
      _promptController.clear();
      _isStreaming = true;
    });

    await Future.delayed(const Duration(milliseconds: 600));

    if (mounted) {
      setState(() {
        _isStreaming = false;
        _messages.add({
          'role': 'assistant',
          'content': 'I evaluated your request. I propose modifying build.gradle.kts to update dependencies.',
          'has_tool_call': 'true',
          'tool_name': 'edit_file',
          'target_path': 'build.gradle.kts',
          'diff_preview': '+ implementation("androidx.core:core-ktx:1.12.0")',
        });
      });
    }
  }

  void _reviewToolCall(Map<String, String> msg) async {
    final approved = await showDialog<bool>(
      context: context,
      builder: (context) => AiToolApprovalDialog(
        toolName: msg['tool_name'] ?? 'edit_file',
        targetPath: msg['target_path'] ?? 'build.gradle.kts',
        diffPreview: msg['diff_preview'] ?? '+ edit',
      ),
    );

    if (approved == true && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Tool call approved and executed on remote host!'),
          backgroundColor: Colors.green,
        ),
      );
    }
  }

  @override
  void dispose() {
    _promptController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // Model Selection Dropdown Header
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
          color: const Color(0xFF252526),
          child: Row(
            children: [
              const Icon(Icons.psychology, color: Colors.amber),
              const SizedBox(width: 8),
              DropdownButton<String>(
                value: _selectedModel,
                underline: const SizedBox(),
                items: const [
                  DropdownMenuItem(
                    value: 'Gemini 3.8 Flash (Antigravity)',
                    child: Text('Gemini 3.8 Flash (Antigravity)'),
                  ),
                  DropdownMenuItem(
                    value: 'Gemini 3.6 Flash (Antigravity)',
                    child: Text('Gemini 3.6 Flash (Antigravity)'),
                  ),
                  DropdownMenuItem(
                    value: 'Claude 3.7 Sonnet',
                    child: Text('Claude 3.7 Sonnet (Direct API)'),
                  ),
                  DropdownMenuItem(
                    value: 'GPT-4o',
                    child: Text('GPT-4o (Direct API)'),
                  ),
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

        // Message List
        Expanded(
          child: ListView.builder(
            padding: const EdgeInsets.all(16.0),
            itemCount: _messages.length,
            itemBuilder: (context, index) {
              final msg = _messages[index];
              final isUser = msg['role'] == 'user';
              final hasToolCall = msg['has_tool_call'] == 'true';

              return Align(
                alignment: isUser ? Alignment.centerRight : Alignment.centerLeft,
                child: Container(
                  margin: const EdgeInsets.symmetric(vertical: 4.0),
                  padding: const EdgeInsets.all(12.0),
                  decoration: BoxDecoration(
                    color: isUser ? Colors.purple[900] : const Color(0xFF252526),
                    borderRadius: BorderRadius.circular(8.0),
                  ),
                  constraints: const BoxConstraints(maxWidth: 320),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        isUser ? 'You' : _selectedModel,
                        style: TextStyle(
                          fontWeight: FontWeight.bold,
                          color: isUser ? Colors.purpleAccent : Colors.amber,
                          fontSize: 11,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(msg['content'] ?? ''),
                      if (hasToolCall) ...[
                        const SizedBox(height: 8),
                        ElevatedButton.icon(
                          onPressed: () => _reviewToolCall(msg),
                          icon: const Icon(Icons.gavel, size: 14),
                          label: const Text('Review Proposed Changes'),
                          style: ElevatedButton.styleFrom(
                            backgroundColor: Colors.amber[800],
                            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
              );
            },
          ),
        ),

        if (_isStreaming)
          const Padding(
            padding: EdgeInsets.all(8.0),
            child: Row(
              children: [
                SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2)),
                SizedBox(width: 8),
                Text('AI assistant is thinking...', style: TextStyle(fontSize: 12, color: Colors.grey)),
              ],
            ),
          ),

        // Prompt Input Field
        Padding(
          padding: const EdgeInsets.all(12.0),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _promptController,
                  decoration: const InputDecoration(
                    hintText: 'Ask AI assistant or issue command...',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  onSubmitted: (_) => _sendPrompt(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton(
                icon: const Icon(Icons.send, color: Colors.amber),
                onPressed: _sendPrompt,
              ),
            ],
          ),
        ),
      ],
    );
  }
}

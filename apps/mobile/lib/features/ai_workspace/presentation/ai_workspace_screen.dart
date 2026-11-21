import 'package:flutter/material.dart';

class AiWorkspaceScreen extends StatelessWidget {
  const AiWorkspaceScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
          color: const Color(0xFF252526),
          child: Row(
            children: [
              const Icon(Icons.psychology, color: Colors.amber),
              const SizedBox(width: 8),
              DropdownButton<String>(
                value: 'Gemini 3.8 Flash (Antigravity)',
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
                ],
                onChanged: (val) {},
              ),
            ],
          ),
        ),
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16.0),
            children: const [
              Card(
                child: Padding(
                  padding: EdgeInsets.all(12.0),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Assistant', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.amber)),
                      SizedBox(height: 4),
                      Text('DevOrbit AI Workspace ready. Connected to Antigravity ACP Agent.'),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(12.0),
          child: Row(
            children: [
              const Expanded(
                child: TextField(
                  decoration: InputDecoration(
                    hintText: 'Ask AI assistant or issue command...',
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              IconButton(
                icon: const Icon(Icons.send, color: Colors.amber),
                onPressed: () {},
              ),
            ],
          ),
        ),
      ],
    );
  }
}

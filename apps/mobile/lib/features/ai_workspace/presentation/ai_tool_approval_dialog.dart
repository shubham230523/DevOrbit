import 'package:flutter/material.dart';

class AiToolApprovalDialog extends StatelessWidget {
  final String toolName;
  final String targetPath;
  final String diffPreview;

  const AiToolApprovalDialog({
    super.key,
    required this.toolName,
    required this.targetPath,
    required this.diffPreview,
  });

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Row(
        children: [
          Icon(Icons.gavel, color: Colors.amber),
          SizedBox(width: 8),
          Text('AI Tool Call Approval Required'),
        ],
      ),
      content: SizedBox(
        width: double.maxFinite,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Action: $toolName', style: const TextStyle(fontWeight: FontWeight.bold, color: Colors.amber)),
            const SizedBox(height: 4),
            Text('Target: $targetPath', style: const TextStyle(fontSize: 12, color: Colors.grey)),
            const SizedBox(height: 12),
            const Text('Proposed Diff Preview:', style: TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 4),
            Container(
              padding: const EdgeInsets.all(8.0),
              color: Colors.black26,
              height: 100,
              width: double.infinity,
              child: SingleChildScrollView(
                child: Text(
                  diffPreview,
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 11, color: Colors.greenAccent),
                ),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Reject Tool Call', style: TextStyle(color: Colors.redAccent)),
        ),
        ElevatedButton(
          onPressed: () => Navigator.of(context).pop(true),
          style: ElevatedButton.styleFrom(backgroundColor: Colors.amber[800]),
          child: const Text('Approve & Apply to Host'),
        ),
      ],
    );
  }
}

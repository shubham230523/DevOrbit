import 'package:flutter/material.dart';

class TravelApprovalDialog extends StatelessWidget {
  final String taskId;
  final String toolName;
  final String? targetPath;
  final String? expectedSHA256;
  final String? targetContent;
  final String? replacement;
  final String? command;
  final List<dynamic>? args;
  final String reason;

  const TravelApprovalDialog({
    super.key,
    required this.taskId,
    required this.toolName,
    this.targetPath,
    this.expectedSHA256,
    this.targetContent,
    this.replacement,
    this.command,
    this.args,
    required this.reason,
  });

  @override
  Widget build(BuildContext context) {
    final isCommand = toolName == 'execute_command';

    return AlertDialog(
      backgroundColor: const Color(0xFF252526),
      title: Row(
        children: [
          Icon(
            isCommand ? Icons.terminal : Icons.gavel,
            color: isCommand ? Colors.orangeAccent : Colors.amber,
          ),
          const SizedBox(width: 8),
          Text(
            isCommand ? 'Command Approval' : 'File Change Approval',
            style: const TextStyle(color: Colors.white, fontSize: 18),
          ),
        ],
      ),
      content: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              reason,
              style: const TextStyle(fontWeight: FontWeight.bold, color: Colors.amber),
            ),
            const SizedBox(height: 12),
            if (targetPath != null && targetPath!.isNotEmpty) ...[
              const Text('Target File:', style: TextStyle(color: Colors.grey, fontSize: 12)),
              Text(targetPath!, style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
              const SizedBox(height: 8),
            ],
            if (expectedSHA256 != null && expectedSHA256!.isNotEmpty) ...[
              const Text('Base SHA256 Hash:', style: TextStyle(color: Colors.grey, fontSize: 12)),
              Text(
                expectedSHA256!,
                style: const TextStyle(color: Colors.grey, fontSize: 10, fontFamily: 'monospace'),
              ),
              const SizedBox(height: 8),
            ],
            if (command != null && command!.isNotEmpty) ...[
              const Text('Command Line:', style: TextStyle(color: Colors.grey, fontSize: 12)),
              Container(
                padding: const EdgeInsets.all(8.0),
                color: Colors.black,
                width: double.infinity,
                child: Text(
                  '$command ${args?.join(" ") ?? ""}',
                  style: const TextStyle(color: Colors.greenAccent, fontFamily: 'monospace', fontSize: 12),
                ),
              ),
              const SizedBox(height: 8),
            ],
            if (targetContent != null && targetContent!.isNotEmpty) ...[
              const Text('Target Snippet to Replace:', style: TextStyle(color: Colors.grey, fontSize: 12)),
              Container(
                padding: const EdgeInsets.all(8.0),
                color: Colors.red[950],
                width: double.infinity,
                child: Text(
                  '- $targetContent',
                  style: const TextStyle(color: Colors.redAccent, fontFamily: 'monospace', fontSize: 11),
                ),
              ),
              const SizedBox(height: 8),
            ],
            if (replacement != null && replacement!.isNotEmpty) ...[
              const Text('Replacement Snippet:', style: TextStyle(color: Colors.grey, fontSize: 12)),
              Container(
                padding: const EdgeInsets.all(8.0),
                color: Colors.green[950],
                width: double.infinity,
                child: Text(
                  '+ $replacement',
                  style: const TextStyle(color: Colors.greenAccent, fontFamily: 'monospace', fontSize: 11),
                ),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Reject', style: TextStyle(color: Colors.redAccent)),
        ),
        ElevatedButton(
          style: ElevatedButton.styleFrom(backgroundColor: Colors.green),
          onPressed: () => Navigator.of(context).pop(true),
          child: const Text('Approve & Execute'),
        ),
      ],
    );
  }
}

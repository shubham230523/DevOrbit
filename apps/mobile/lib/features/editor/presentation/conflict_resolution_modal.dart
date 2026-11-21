import 'package:flutter/material.dart';

class ConflictResolutionModal extends StatelessWidget {
  final String filePath;
  final String localContent;
  final String remoteContent;
  final VoidCallback onKeepLocal;
  final VoidCallback onAcceptRemote;

  const ConflictResolutionModal({
    super.key,
    required this.filePath,
    required this.localContent,
    required this.remoteContent,
    required this.onKeepLocal,
    required this.onAcceptRemote,
  });

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Row(
        children: [
          Icon(Icons.warning_amber_rounded, color: Colors.orangeAccent),
          SizedBox(width: 8),
          Text('File Edit Conflict Detected'),
        ],
      ),
      content: SizedBox(
        width: double.maxFinite,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'The file "$filePath" was modified on the host machine since you opened it.',
              style: const TextStyle(fontSize: 13),
            ),
            const SizedBox(height: 12),
            const Text('Local Edit:', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.purpleAccent)),
            Container(
              padding: const EdgeInsets.all(8.0),
              color: Colors.black26,
              height: 80,
              width: double.infinity,
              child: SingleChildScrollView(
                child: Text(localContent, style: const TextStyle(fontFamily: 'monospace', fontSize: 11)),
              ),
            ),
            const SizedBox(height: 8),
            const Text('Remote Host Version:', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.tealAccent)),
            Container(
              padding: const EdgeInsets.all(8.0),
              color: Colors.black26,
              height: 80,
              width: double.infinity,
              child: SingleChildScrollView(
                child: Text(remoteContent, style: const TextStyle(fontFamily: 'monospace', fontSize: 11)),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () {
            Navigator.of(context).pop();
            onAcceptRemote();
          },
          child: const Text('Accept Remote Version'),
        ),
        ElevatedButton(
          onPressed: () {
            Navigator.of(context).pop();
            onKeepLocal();
          },
          style: ElevatedButton.styleFrom(backgroundColor: Colors.purple),
          child: const Text('Keep Local Edits (Force Save)'),
        ),
      ],
    );
  }
}

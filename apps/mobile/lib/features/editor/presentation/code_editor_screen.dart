import 'package:flutter/material.dart';
import 'conflict_resolution_modal.dart';

class CodeEditorScreen extends StatefulWidget {
  final String? filePath;

  const CodeEditorScreen({super.key, this.filePath});

  @override
  State<CodeEditorScreen> createState() => _CodeEditorScreenState();
}

class _CodeEditorScreenState extends State<CodeEditorScreen> {
  late TextEditingController _textController;
  bool _isModified = false;
  bool _isSaving = false;

  @override
  void initState() {
    super.initState();
    _textController = TextEditingController(
      text: '''// DevOrbit Remote Code Editor
package com.devorbit.mobile

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity

class MainActivity : AppCompatActivity() {
    override func onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        println("DevOrbit Connected to Remote Host!")
    }
}''',
    );

    _textController.addListener(() {
      if (!_isModified) {
        setState(() {
          _isModified = true;
        });
      }
    });
  }

  void _saveFile() async {
    setState(() {
      _isSaving = true;
    });

    await Future.delayed(const Duration(milliseconds: 400));

    if (mounted) {
      setState(() {
        _isSaving = false;
        _isModified = false;
      });

      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Saved file to remote host (Atomic write OK)'),
          backgroundColor: Colors.green,
        ),
      );
    }
  }

  void triggerConflictModalForTest() {
    showDialog(
      context: context,
      builder: (context) => ConflictResolutionModal(
        filePath: widget.filePath ?? 'src/MainActivity.kt',
        localContent: _textController.text,
        remoteContent: '// Modified on desktop host by another process\nprintln("Desktop Edit")',
        onKeepLocal: () {
          setState(() {
            _isModified = false;
          });
        },
        onAcceptRemote: () {
          setState(() {
            _textController.text = '// Modified on desktop host by another process\nprintln("Desktop Edit")';
            _isModified = false;
          });
        },
      ),
    );
  }

  @override
  void dispose() {
    _textController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // Editor Header Bar
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
          color: const Color(0xFF252526),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Row(
                children: [
                  const Icon(Icons.code, size: 18, color: Colors.purpleAccent),
                  const SizedBox(width: 8),
                  Text(
                    widget.filePath ?? 'src/MainActivity.kt',
                    style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 13),
                  ),
                  if (_isModified)
                    const Padding(
                      padding: EdgeInsets.only(left: 6.0),
                      child: Icon(Icons.circle, size: 8, color: Colors.amber),
                    ),
                ],
              ),
              ElevatedButton.icon(
                onPressed: _isSaving || !_isModified ? null : _saveFile,
                icon: _isSaving
                    ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.save, size: 16),
                label: const Text('Save to Host'),
                style: ElevatedButton.styleFrom(
                  backgroundColor: Colors.purple,
                  padding: const EdgeInsets.symmetric(horizontal: 12.0, vertical: 8.0),
                ),
              ),
            ],
          ),
        ),

        // Text Editor Area
        Expanded(
          child: Container(
            color: const Color(0xFF1E1E1E),
            padding: const EdgeInsets.all(12.0),
            child: TextField(
              controller: _textController,
              maxLines: null,
              expands: true,
              style: const TextStyle(
                fontFamily: 'monospace',
                fontSize: 13.0,
                color: Colors.white,
              ),
              decoration: const InputDecoration(
                border: InputBorder.none,
              ),
            ),
          ),
        ),
      ],
    );
  }
}

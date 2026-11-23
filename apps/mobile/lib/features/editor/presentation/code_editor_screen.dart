import 'package:flutter/material.dart';
import 'conflict_resolution_modal.dart';

class CodeEditorScreen extends StatefulWidget {
  final String? filePath;
  final String? initialContent;

  const CodeEditorScreen({
    super.key,
    this.filePath,
    this.initialContent,
  });

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
    _textController = TextEditingController(text: widget.initialContent ?? '');

    _textController.addListener(() {
      if (!_isModified && _textController.text != (widget.initialContent ?? '')) {
        setState(() {
          _isModified = true;
        });
      }
    });
  }

  @override
  void didUpdateWidget(covariant CodeEditorScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.filePath != widget.filePath || oldWidget.initialContent != widget.initialContent) {
      _textController.text = widget.initialContent ?? '';
      setState(() {
        _isModified = false;
      });
    }
  }

  void _saveFile() async {
    if (widget.filePath == null) return;

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
        SnackBar(
          content: Text('Saved "${widget.filePath}" to host.'),
          backgroundColor: Colors.green,
        ),
      );
    }
  }

  void triggerConflictModalForTest() {
    showDialog(
      context: context,
      builder: (context) => ConflictResolutionModal(
        filePath: widget.filePath ?? 'src/File.kt',
        localContent: _textController.text,
        remoteContent: '// Modified on host by another process',
        onKeepLocal: () {
          setState(() {
            _isModified = false;
          });
        },
        onAcceptRemote: () {
          setState(() {
            _textController.text = '// Modified on host by another process';
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
    if (widget.filePath == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32.0),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(Icons.edit_note, size: 56, color: Colors.grey.shade600),
              const SizedBox(height: 12),
              const Text(
                'No file open in editor.',
                style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
              ),
              const SizedBox(height: 6),
              const Text(
                'Select a file from Project Explorer to view or edit its contents.',
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 12, color: Colors.grey),
              ),
            ],
          ),
        ),
      );
    }

    return Column(
      children: [
        // Editor Header Bar
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16.0, vertical: 8.0),
          color: const Color(0xFF252526),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Expanded(
                child: Row(
                  children: [
                    const Icon(Icons.code, size: 18, color: Colors.purpleAccent),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        widget.filePath!,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 13),
                      ),
                    ),
                    if (_isModified)
                      const Padding(
                        padding: EdgeInsets.only(left: 6.0),
                        child: Icon(Icons.circle, size: 8, color: Colors.amber),
                      ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
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
                hintText: 'Enter code or text...',
              ),
            ),
          ),
        ),
      ],
    );
  }
}


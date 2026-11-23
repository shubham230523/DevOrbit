import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';
import '../../../core/security/secure_storage.dart';
import 'conflict_resolution_modal.dart';

class CodeEditorScreen extends StatefulWidget {
  final String? filePath;
  final String? workspaceId;
  final String? initialContent;

  const CodeEditorScreen({
    super.key,
    this.filePath,
    this.workspaceId,
    this.initialContent,
  });

  @override
  State<CodeEditorScreen> createState() => _CodeEditorScreenState();
}

class _CodeEditorScreenState extends State<CodeEditorScreen> {
  late TextEditingController _textController;
  bool _isModified = false;
  bool _isLoading = false;
  bool _isSaving = false;
  String? _baseSHA256;
  String? _errorMessage;

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

    if (widget.filePath != null && widget.initialContent == null) {
      _loadRemoteFileContent();
    }
  }

  @override
  void didUpdateWidget(covariant CodeEditorScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.filePath != widget.filePath || oldWidget.initialContent != widget.initialContent) {
      _textController.text = widget.initialContent ?? '';
      setState(() {
        _isModified = false;
        _errorMessage = null;
      });

      if (widget.filePath != null && widget.initialContent == null) {
        _loadRemoteFileContent();
      }
    }
  }

  Future<void> _loadRemoteFileContent() async {
    if (widget.filePath == null) return;

    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final session = await SecureStorageService.instance.getPairedSession();
    if (session == null || session['brokerUrl'] == null) {
      if (mounted) {
        setState(() {
          _isLoading = false;
        });
      }
      return;
    }

    final client = HostBridgeApiClient(
      baseUrl: session['brokerUrl'] ?? 'http://localhost:8082',
      sessionToken: session['token'],
    );

    final wsId = widget.workspaceId ?? (session['machineId'] ?? 'workspace');
    final res = await client.readFile(wsId, widget.filePath!);

    if (res != null && mounted) {
      setState(() {
        _textController.text = res['content'] ?? '';
        _baseSHA256 = res['sha256_hash'];
        _isLoading = false;
        _isModified = false;
      });
    } else if (mounted) {
      setState(() {
        _isLoading = false;
        _errorMessage = 'Failed to load file content from host bridge.';
      });
    }
  }

  Future<void> _saveFile() async {
    if (widget.filePath == null) return;

    setState(() {
      _isSaving = true;
      _errorMessage = null;
    });

    final session = await SecureStorageService.instance.getPairedSession();
    if (session == null || session['brokerUrl'] == null) {
      if (mounted) {
        setState(() {
          _isSaving = false;
          _errorMessage = 'No active host session. Pair machine to save changes.';
        });
      }
      return;
    }

    final client = HostBridgeApiClient(
      baseUrl: session['brokerUrl'] ?? 'http://localhost:8082',
      sessionToken: session['token'],
    );

    final wsId = widget.workspaceId ?? (session['machineId'] ?? 'workspace');
    final res = await client.writeFile(
      workspaceId: wsId,
      relativePath: widget.filePath!,
      newContent: _textController.text,
      baseSHA256: _baseSHA256 ?? '',
    );

    if (mounted) {
      setState(() {
        _isSaving = false;
      });

      if (res['success'] == true) {
        final data = res['data'] as Map<String, dynamic>?;
        if (data != null && data['updated_sha256'] != null) {
          _baseSHA256 = data['updated_sha256'];
        }
        setState(() {
          _isModified = false;
        });

        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Saved "${widget.filePath}" to remote host.'),
            backgroundColor: Colors.green,
          ),
        );
      } else if (res['conflict'] == true) {
        final data = res['data'] as Map<String, dynamic>?;
        _handleSaveConflict(data?['current_content'] ?? '');
      } else {
        setState(() {
          _errorMessage = res['error'] ?? 'Failed to save file to host.';
        });
      }
    }
  }

  void _handleSaveConflict(String remoteContent) {
    showDialog(
      context: context,
      builder: (context) => ConflictResolutionModal(
        filePath: widget.filePath!,
        localContent: _textController.text,
        remoteContent: remoteContent,
        onKeepLocal: () {
          _saveFileForce();
        },
        onAcceptRemote: () {
          setState(() {
            _textController.text = remoteContent;
            _isModified = false;
          });
        },
      ),
    );
  }

  Future<void> _saveFileForce() async {
    final session = await SecureStorageService.instance.getPairedSession();
    if (session == null || session['brokerUrl'] == null) return;

    final client = HostBridgeApiClient(
      baseUrl: session['brokerUrl'] ?? 'http://localhost:8082',
      sessionToken: session['token'],
    );

    final wsId = widget.workspaceId ?? (session['machineId'] ?? 'workspace');
    final res = await client.writeFile(
      workspaceId: wsId,
      relativePath: widget.filePath!,
      newContent: _textController.text,
      baseSHA256: '', // Empty base SHA bypasses optimistic lock
    );

    if (res['success'] == true && mounted) {
      setState(() {
        _isModified = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Overwrote remote host file.'), backgroundColor: Colors.orange),
      );
    }
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
                'Select a file from Project Explorer or Travel AI Cockpit to view or edit its contents.',
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

        if (_errorMessage != null)
          Container(
            padding: const EdgeInsets.all(8.0),
            color: Colors.red[900],
            width: double.infinity,
            child: Text(_errorMessage!, style: const TextStyle(color: Colors.white, fontSize: 12)),
          ),

        // Text Editor Area
        Expanded(
          child: _isLoading
              ? const Center(child: CircularProgressIndicator())
              : Container(
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



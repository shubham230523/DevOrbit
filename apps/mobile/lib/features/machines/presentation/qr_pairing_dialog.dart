import 'package:flutter/material.dart';

class QrPairingDialog extends StatefulWidget {
  const QrPairingDialog({super.key});

  @override
  State<QrPairingDialog> createState() => _QrPairingDialogState();
}

class _QrPairingDialogState extends State<QrPairingDialog> {
  bool _isPairing = false;
  String? _statusText;

  Future<void> _simulateQrScanAndPair() async {
    setState(() {
      _isPairing = true;
      _statusText = 'Scanning QR pairing payload...';
    });

    await Future.delayed(const Duration(milliseconds: 500));

    setState(() {
      _statusText = 'Verifying host identity & submitting proof of possession...';
    });

    await Future.delayed(const Duration(milliseconds: 600));

    if (mounted) {
      Navigator.of(context).pop(true);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Row(
        children: [
          Icon(Icons.qr_code_scanner, color: Colors.purpleAccent),
          SizedBox(width: 8),
          Text('Pair Host Machine'),
        ],
      ),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            height: 180,
            width: 180,
            decoration: BoxDecoration(
              color: Colors.grey[900],
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: Colors.purpleAccent),
            ),
            child: const Center(
              child: Icon(Icons.qr_code_2, size: 120, color: Colors.white),
            ),
          ),
          const SizedBox(height: 16),
          if (_statusText != null)
            Text(
              _statusText!,
              textAlign: TextAlign.center,
              style: const TextStyle(fontSize: 12, color: Colors.grey),
            )
          else
            const Text(
              'Scan QR Code displayed on DevOrbit Host Bridge CLI / GUI',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 13),
            ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: _isPairing ? null : () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        ElevatedButton(
          onPressed: _isPairing ? null : _simulateQrScanAndPair,
          child: _isPairing
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Scan & Pair'),
        ),
      ],
    );
  }
}

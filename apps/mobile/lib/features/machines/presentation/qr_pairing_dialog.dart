import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import '../../../core/pairing/pairing_client.dart';
import '../../../core/pairing/qr_pairing_payload.dart';
import '../../../core/security/secure_storage.dart';

class QrPairingDialog extends StatefulWidget {
  final PairingClient? pairingClient;
  final SecureStorageService? secureStorage;
  final String? testDeviceId;

  const QrPairingDialog({
    super.key,
    this.pairingClient,
    this.secureStorage,
    this.testDeviceId,
  });

  @override
  State<QrPairingDialog> createState() => _QrPairingDialogState();
}

class _QrPairingDialogState extends State<QrPairingDialog> {
  late final PairingClient _pairingClient;
  late final SecureStorageService _storage;
  MobileScannerController? _scannerController;

  bool _isPairing = false;
  String? _statusText;
  String? _errorMessage;
  bool _showManualInput = false;
  final TextEditingController _manualInputController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _pairingClient = widget.pairingClient ?? PairingClient();
    _storage = widget.secureStorage ?? SecureStorageService.instance;
    _scannerController = MobileScannerController(
      detectionSpeed: DetectionSpeed.noDuplicates,
      facing: CameraFacing.back,
    );
  }

  @override
  void dispose() {
    _manualInputController.dispose();
    _scannerController?.dispose();
    super.dispose();
  }

  Future<void> processPairingPayload(String rawPayload) async {
    if (_isPairing) return;

    setState(() {
      _isPairing = true;
      _errorMessage = null;
      _statusText = 'Validating QR pairing payload...';
    });

    QrPairingPayload payload;
    try {
      payload = QrPairingPayload.fromRawString(rawPayload);
    } catch (e) {
      if (mounted) {
        setState(() {
          _isPairing = false;
          _errorMessage = 'Invalid QR code: ${e.toString().replaceAll("FormatException: ", "")}';
          _statusText = null;
        });
      }
      return;
    }

    if (payload.isExpired) {
      if (mounted) {
        setState(() {
          _isPairing = false;
          _errorMessage = 'Pairing challenge expired at ${payload.expiresAt.toLocal()}. Please generate a fresh QR code.';
          _statusText = null;
        });
      }
      return;
    }

    setState(() {
      _statusText = 'Verifying pairing with ${payload.hostName} (${payload.hostId})...';
    });

    final deviceId = widget.testDeviceId ?? 'mobile-${DateTime.now().millisecondsSinceEpoch % 1000000}';
    const deviceName = 'DevOrbit Mobile Client';

    try {
      await _pairingClient.confirmPairing(
        payload: payload,
        deviceId: deviceId,
        deviceName: deviceName,
        secureStorage: _storage,
      );

      if (mounted) {
        setState(() {
          _statusText = 'Pairing confirmed! Credentials secured.';
        });
        Navigator.of(context).pop(true);
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _isPairing = false;
          _statusText = null;
          if (e is PairingException) {
            _errorMessage = e.message;
          } else {
            _errorMessage = 'Pairing error: $e';
          }
        });
      }
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
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (_errorMessage != null) ...[
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(12),
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: Colors.red.shade900.withValues(alpha: 0.3),
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: Colors.redAccent),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.error_outline, color: Colors.redAccent, size: 20),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        _errorMessage!,
                        style: const TextStyle(color: Colors.redAccent, fontSize: 12),
                      ),
                    ),
                  ],
                ),
              ),
            ],
            if (!_showManualInput) ...[
              Container(
                height: 220,
                width: 220,
                decoration: BoxDecoration(
                  color: Colors.black,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: Colors.purpleAccent),
                ),
                clipBehavior: Clip.antiAlias,
                child: Stack(
                  alignment: Alignment.center,
                  children: [
                    MobileScanner(
                      controller: _scannerController,
                      onDetect: (capture) {
                        final barcodes = capture.barcodes;
                        for (final barcode in barcodes) {
                          if (barcode.rawValue != null) {
                            processPairingPayload(barcode.rawValue!);
                            break;
                          }
                        }
                      },
                      errorBuilder: (context, error, child) {
                        return Center(
                          child: Padding(
                            padding: const EdgeInsets.all(8.0),
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                const Icon(Icons.camera_alt_outlined, color: Colors.grey, size: 48),
                                const SizedBox(height: 8),
                                Text(
                                  'Camera unavailable in test/desktop mode: ${error.errorCode}',
                                  textAlign: TextAlign.center,
                                  style: const TextStyle(fontSize: 11, color: Colors.grey),
                                ),
                              ],
                            ),
                          ),
                        );
                      },
                    ),
                    if (_isPairing)
                      Container(
                        color: Colors.black54,
                        child: const Center(
                          child: CircularProgressIndicator(),
                        ),
                      ),
                  ],
                ),
              ),
              const SizedBox(height: 12),
              if (_statusText != null)
                Text(
                  _statusText!,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 12, color: Colors.purpleAccent),
                )
              else
                const Text(
                  'Align camera with QR code on DevOrbit Host Bridge CLI or GUI',
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: 12, color: Colors.grey),
                ),
            ] else ...[
              TextField(
                controller: _manualInputController,
                maxLines: 5,
                decoration: const InputDecoration(
                  labelText: 'Paste QR Pairing Payload JSON',
                  border: OutlineInputBorder(),
                  hintText: '{"v":"1.0","challenge_id":"...","host_id":"...",...}',
                ),
              ),
              const SizedBox(height: 8),
              ElevatedButton(
                onPressed: _isPairing
                    ? null
                    : () {
                        final text = _manualInputController.text.trim();
                        if (text.isNotEmpty) {
                          processPairingPayload(text);
                        }
                      },
                child: _isPairing
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Submit Pairing Payload'),
              ),
            ],
            const SizedBox(height: 8),
            TextButton(
              onPressed: () {
                setState(() {
                  _showManualInput = !_showManualInput;
                  _errorMessage = null;
                });
              },
              child: Text(
                _showManualInput ? 'Switch to Camera Scanner' : 'Enter Payload Manually / Test',
                style: const TextStyle(fontSize: 12),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: _isPairing ? null : () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
      ],
    );
  }
}

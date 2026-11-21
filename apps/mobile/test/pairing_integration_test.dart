import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:devorbit_mobile/core/pairing/pairing_client.dart';
import 'package:devorbit_mobile/core/pairing/qr_pairing_payload.dart';
import 'package:devorbit_mobile/core/security/secure_storage.dart';
import 'package:devorbit_mobile/features/machines/presentation/qr_pairing_dialog.dart';
import 'helpers/test_secure_storage.dart';

void main() {
  group('QR Pairing Payload & Client Tests', () {
    late TestInMemoryStorageDriver driver;
    late SecureStorageService storage;

    setUp(() {
      driver = TestInMemoryStorageDriver();
      storage = SecureStorageService(driver: driver);
    });

    test('Parses valid versioned QR payload and detects expiry', () {
      final validJson = {
        'v': '1.0',
        'challenge_id': 'qr-12345678',
        'host_id': 'host-desktop-01',
        'host_name': 'Desktop Workstation',
        'pairing_payload': '0123456789abcdef0123456789abcdef',
        'pairing_secret': 'ephemeral-secret-32b',
        'confirm_url': 'http://127.0.0.1:8080/api/v1/pairing/confirm',
        'broker_url': 'ws://127.0.0.1:8081',
        'expires_at': DateTime.now().add(const Duration(minutes: 5)).toIso8601String(),
      };

      final payload = QrPairingPayload.fromJson(validJson);
      expect(payload.version, equals('1.0'));
      expect(payload.challengeId, equals('qr-12345678'));
      expect(payload.hostId, equals('host-desktop-01'));
      expect(payload.isExpired, isFalse);
    });

    test('Rejects unsupported version or missing fields', () {
      expect(
        () => QrPairingPayload.fromJson({
          'v': '2.0',
          'challenge_id': 'ch-01',
          'host_id': 'h-01',
          'pairing_payload': 'p-01',
          'confirm_url': 'http://localhost',
          'expires_at': DateTime.now().toIso8601String(),
        }),
        throwsA(isA<FormatException>()),
      );

      expect(
        () => QrPairingPayload.fromJson({
          'v': '1.0',
          'host_id': 'h-01',
        }),
        throwsA(isA<FormatException>()),
      );
    });

    test('Rejects expired pairing challenge before network request', () async {
      final expiredPayload = QrPairingPayload(
        version: '1.0',
        challengeId: 'qr-exp-01',
        hostId: 'host-exp-01',
        hostName: 'Host Exp',
        pairingPayload: 'payload-exp',
        pairingSecret: 'secret-exp',
        confirmUrl: 'http://127.0.0.1:8080',
        expiresAt: DateTime.now().subtract(const Duration(seconds: 10)),
      );

      final client = PairingClient();
      expect(
        () => client.confirmPairing(
          payload: expiredPayload,
          deviceId: 'dev-01',
          deviceName: 'Phone',
          secureStorage: storage,
        ),
        throwsA(isA<PairingException>()),
      );
    });

    test('Successful backend confirmation persists credentials and survives restart', () async {
      final payload = QrPairingPayload(
        version: '1.0',
        challengeId: 'qr-test-01',
        hostId: 'host-win-01',
        hostName: 'Windows Desktop',
        pairingPayload: 'payload-hex-32b',
        pairingSecret: 'secret-ephemeral-32b',
        confirmUrl: 'http://127.0.0.1:8080/api/v1/pairing/confirm',
        brokerUrl: 'ws://127.0.0.1:8081',
        expiresAt: DateTime.now().add(const Duration(minutes: 5)),
      );

      final mockHttp = MockClient((request) async {
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        expect(body['challenge_id'], equals('qr-test-01'));
        expect(body['device_id'], equals('dev-phone-01'));
        expect(body['proof_signature'], isNotEmpty);

        return http.Response(
          jsonEncode({
            'status': 'success',
            'session_token': 'token-issued-by-backend-32b',
            'session_id': 'sess-backend-issued-101',
            'session_secret': 'master-secret-issued-by-backend-32b',
            'machine_id': 'host-win-01',
            'device_id': 'dev-phone-01',
            'broker_url': 'ws://127.0.0.1:8081',
            'expires_at': DateTime.now().add(const Duration(hours: 24)).toIso8601String(),
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      });

      final client = PairingClient(httpClient: mockHttp);
      final result = await client.confirmPairing(
        payload: payload,
        deviceId: 'dev-phone-01',
        deviceName: 'Pixel Phone',
        secureStorage: storage,
      );

      expect(result.sessionId, equals('sess-backend-issued-101'));
      expect(result.token, equals('token-issued-by-backend-32b'));

      // Verify persistent recovery across service instances (simulated restart)
      final recoveredStorage = SecureStorageService(driver: driver);
      final saved = await recoveredStorage.getPairedSession();

      expect(saved, isNotNull);
      expect(saved!['machineId'], equals('host-win-01'));
      expect(saved['deviceId'], equals('dev-phone-01'));
      expect(saved['sessionId'], equals('sess-backend-issued-101'));
      expect(saved['token'], equals('token-issued-by-backend-32b'));
      expect(saved['masterSecret'], equals('master-secret-issued-by-backend-32b'));
      expect(saved['brokerUrl'], equals('ws://127.0.0.1:8081'));
    });

    test('Backend challenge consumed / replay error surfaces actionable message', () async {
      final payload = QrPairingPayload(
        version: '1.0',
        challengeId: 'qr-consumed-01',
        hostId: 'host-win-01',
        hostName: 'Windows Desktop',
        pairingPayload: 'payload-hex-32b',
        pairingSecret: 'secret-ephemeral-32b',
        confirmUrl: 'http://127.0.0.1:8080/api/v1/pairing/confirm',
        expiresAt: DateTime.now().add(const Duration(minutes: 5)),
      );

      final mockHttp = MockClient((request) async {
        return http.Response(
          jsonEncode({'error': 'pairing challenge already consumed'}),
          400,
          headers: {'content-type': 'application/json'},
        );
      });

      final client = PairingClient(httpClient: mockHttp);
      try {
        await client.confirmPairing(
          payload: payload,
          deviceId: 'dev-01',
          deviceName: 'Phone',
          secureStorage: storage,
        );
        fail('Expected PairingException');
      } catch (e) {
        expect(e, isA<PairingException>());
        expect(
          (e as PairingException).message,
          contains('already been consumed'),
        );
      }
    });
  });

  group('QrPairingDialog Widget & UI Tests', () {
    late TestInMemoryStorageDriver driver;
    late SecureStorageService storage;

    setUp(() {
      driver = TestInMemoryStorageDriver();
      storage = SecureStorageService(driver: driver);
    });

    testWidgets('Submitting valid payload via dialog confirms pairing and closes with true', (tester) async {
      final validPayloadJson = jsonEncode({
        'v': '1.0',
        'challenge_id': 'qr-dialog-01',
        'host_id': 'host-dialog-01',
        'host_name': 'Desktop Host',
        'pairing_payload': 'payload-dialog-32b',
        'pairing_secret': 'secret-dialog-32b',
        'confirm_url': 'http://127.0.0.1:8080/api/v1/pairing/confirm',
        'broker_url': 'ws://127.0.0.1:8081',
        'expires_at': DateTime.now().add(const Duration(minutes: 5)).toIso8601String(),
      });

      final mockHttp = MockClient((request) async {
        return http.Response(
          jsonEncode({
            'status': 'success',
            'session_token': 'token-dialog-secret-32b',
            'session_id': 'sess-dialog-101',
            'session_secret': 'master-dialog-secret-32b',
            'machine_id': 'host-dialog-01',
            'device_id': 'mobile-test-dialog',
            'broker_url': 'ws://127.0.0.1:8081',
            'expires_at': DateTime.now().add(const Duration(hours: 24)).toIso8601String(),
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      });

      final pairingClient = PairingClient(httpClient: mockHttp);

      bool? dialogResult;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) {
              return ElevatedButton(
                onPressed: () async {
                  dialogResult = await showDialog<bool>(
                    context: context,
                    builder: (ctx) => QrPairingDialog(
                      pairingClient: pairingClient,
                      secureStorage: storage,
                      testDeviceId: 'mobile-test-dialog',
                    ),
                  );
                },
                child: const Text('Open Dialog'),
              );
            },
          ),
        ),
      );

      // Open dialog
      await tester.tap(find.text('Open Dialog'));
      await tester.pumpAndSettle();

      expect(find.text('Pair Host Machine'), findsOneWidget);

      // Switch to manual input mode for testing
      await tester.tap(find.text('Enter Payload Manually / Test'));
      await tester.pumpAndSettle();

      expect(find.text('Paste QR Pairing Payload JSON'), findsOneWidget);

      // Enter valid payload JSON
      await tester.enterText(find.byType(TextField), validPayloadJson);
      await tester.tap(find.text('Submit Pairing Payload'));
      await tester.pumpAndSettle();

      // Dialog should close with true
      expect(dialogResult, isTrue);

      // Verify credentials stored securely
      final saved = await storage.getPairedSession();
      expect(saved, isNotNull);
      expect(saved!['sessionId'], equals('sess-dialog-101'));
      expect(saved['token'], equals('token-dialog-secret-32b'));
    });

    testWidgets('Submitting expired payload displays actionable error banner without closing', (tester) async {
      final expiredPayloadJson = jsonEncode({
        'v': '1.0',
        'challenge_id': 'qr-exp-02',
        'host_id': 'host-exp-02',
        'host_name': 'Desktop Host',
        'pairing_payload': 'payload-32b',
        'pairing_secret': 'secret-32b',
        'confirm_url': 'http://127.0.0.1:8080/api/v1/pairing/confirm',
        'expires_at': DateTime.now().subtract(const Duration(minutes: 1)).toIso8601String(),
      });

      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) {
              return ElevatedButton(
                onPressed: () => showDialog<bool>(
                  context: context,
                  builder: (ctx) => QrPairingDialog(
                    secureStorage: storage,
                  ),
                ),
                child: const Text('Open Dialog'),
              );
            },
          ),
        ),
      );

      await tester.tap(find.text('Open Dialog'));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Enter Payload Manually / Test'));
      await tester.pumpAndSettle();

      await tester.enterText(find.byType(TextField), expiredPayloadJson);
      await tester.tap(find.text('Submit Pairing Payload'));
      await tester.pumpAndSettle();

      // Error banner must be visible
      expect(find.textContaining('Pairing challenge expired'), findsOneWidget);
      // Dialog remains open
      expect(find.text('Pair Host Machine'), findsOneWidget);
    });
  });
}

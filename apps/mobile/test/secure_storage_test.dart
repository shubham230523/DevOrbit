import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/core/security/secure_storage.dart';
import 'helpers/test_secure_storage.dart';

void main() {
  group('SecureStorageService Unit & Integrity Tests', () {
    late TestInMemoryStorageDriver driver;
    late SecureStorageService storage;

    setUp(() {
      driver = TestInMemoryStorageDriver();
      storage = SecureStorageService(driver: driver);
    });

    test('Persists and retrieves paired session across service instances (simulated restart)', () async {
      await storage.savePairedSession(
        machineId: 'mach-restart-01',
        deviceId: 'dev-restart-01',
        sessionId: 'sess-restart-01',
        token: 'token-restart-secret-32b',
        masterSecret: 'master-restart-secret-32b-ok!',
        brokerUrl: 'ws://127.0.0.1:8081',
      );

      // Simulate application restart by creating a new SecureStorageService instance
      // connected to the same persistent driver backing store.
      final restartedStorage = SecureStorageService(driver: driver);
      final retrieved = await restartedStorage.getPairedSession();

      expect(retrieved, isNotNull);
      expect(retrieved!['machineId'], equals('mach-restart-01'));
      expect(retrieved['deviceId'], equals('dev-restart-01'));
      expect(retrieved['sessionId'], equals('sess-restart-01'));
      expect(retrieved['token'], equals('token-restart-secret-32b'));
      expect(retrieved['masterSecret'], equals('master-restart-secret-32b-ok!'));
      expect(retrieved['brokerUrl'], equals('ws://127.0.0.1:8081'));
    });

    test('Fails closed on partial write / missing commit signature', () async {
      // Simulate partial write: only write individual keys, omit commit signature
      driver.backingStore[SecureStorageService.keyMachineId] = 'mach-partial-01';
      driver.backingStore[SecureStorageService.keyDeviceId] = 'dev-partial-01';
      driver.backingStore[SecureStorageService.keySessionId] = 'sess-partial-01';

      final session = await storage.getPairedSession();
      // Must reject partial write and return null
      expect(session, isNull);
    });

    test('Fails closed on tampered or mismatched session fields', () async {
      await storage.savePairedSession(
        machineId: 'mach-tamper-01',
        deviceId: 'dev-tamper-01',
        sessionId: 'sess-tamper-01',
        token: 'token-tamper-01',
        masterSecret: 'secret-tamper-01',
      );

      // Tamper with one field in the backing store
      driver.backingStore[SecureStorageService.keyToken] = 'tampered-token-value';

      final session = await storage.getPairedSession();
      // Integrity check must fail and clear corrupt session
      expect(session, isNull);
      expect(driver.backingStore.isEmpty, isTrue);
    });

    test('Throws SecureStorageException on storage read failure (never falls back)', () async {
      await storage.savePairedSession(
        machineId: 'mach-err-01',
        deviceId: 'dev-err-01',
        sessionId: 'sess-err-01',
        token: 'token-err-01',
        masterSecret: 'secret-err-01',
      );

      driver.shouldFailReads = true;
      expect(() async => await storage.getPairedSession(), throwsA(isA<SecureStorageException>()));
    });

    test('Throws SecureStorageException and rolls back on storage write failure', () async {
      driver.shouldFailWrites = true;

      expect(
        () async => await storage.savePairedSession(
          machineId: 'mach-fail-01',
          deviceId: 'dev-fail-01',
          sessionId: 'sess-fail-01',
          token: 'token-fail-01',
          masterSecret: 'secret-fail-01',
        ),
        throwsA(isA<SecureStorageException>()),
      );

      // Ensure no partial data remained
      expect(driver.backingStore[SecureStorageService.keyCommitSignature], isNull);
    });

    test('Clears session on logout or device revocation', () async {
      await storage.savePairedSession(
        machineId: 'mach-clear-01',
        deviceId: 'dev-clear-01',
        sessionId: 'sess-clear-01',
        token: 'token-clear-01',
        masterSecret: 'secret-clear-01',
      );

      expect(await storage.getPairedSession(), isNotNull);

      await storage.clearPairedSession();
      expect(await storage.getPairedSession(), isNull);
      expect(driver.backingStore.isEmpty, isTrue);
    });

    test('Generates deterministic HMAC-SHA256 device proof', () {
      const challengePayload = 'payload-12345';
      const deviceId = 'device-phone-99';
      const secretKey = 'master-secret-key-32b';

      final proof1 = storage.generateDeviceProof(challengePayload, deviceId, secretKey);
      final proof2 = storage.generateDeviceProof(challengePayload, deviceId, secretKey);

      expect(proof1, isNotEmpty);
      expect(proof1, equals(proof2));
    });
  });
}

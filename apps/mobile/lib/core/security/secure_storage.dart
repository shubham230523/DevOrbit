import 'dart:convert';
import 'package:crypto/crypto.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class SecureStorageException implements Exception {
  final String message;
  final dynamic cause;

  SecureStorageException(this.message, [this.cause]);

  @override
  String toString() => 'SecureStorageException: $message ${cause != null ? "($cause)" : ""}';
}

/// Abstract contract for platform-secure key-value storage.
abstract class SecureStorageDriver {
  Future<void> write({required String key, required String value});
  Future<String?> read({required String key});
  Future<void> delete({required String key});
  Future<void> deleteAll();
  Future<Map<String, String>> readAll();
}

/// Production platform-secure storage driver backed by Android Keystore
/// (EncryptedSharedPreferences) and iOS Keychain.
class PlatformSecureStorageDriver implements SecureStorageDriver {
  final FlutterSecureStorage _storage;

  PlatformSecureStorageDriver([FlutterSecureStorage? storage])
      : _storage = storage ??
            const FlutterSecureStorage(
              aOptions: AndroidOptions(
                encryptedSharedPreferences: true,
              ),
              iOptions: IOSOptions(
                accessibility: KeychainAccessibility.first_unlock,
              ),
            );

  @override
  Future<void> write({required String key, required String value}) async {
    try {
      await _storage.write(key: key, value: value);
    } catch (e) {
      throw SecureStorageException('Platform secure storage write failed for key $key', e);
    }
  }

  @override
  Future<String?> read({required String key}) async {
    try {
      return await _storage.read(key: key);
    } catch (e) {
      throw SecureStorageException('Platform secure storage read failed for key $key', e);
    }
  }

  @override
  Future<void> delete({required String key}) async {
    try {
      await _storage.delete(key: key);
    } catch (e) {
      throw SecureStorageException('Platform secure storage delete failed for key $key', e);
    }
  }

  @override
  Future<void> deleteAll() async {
    try {
      await _storage.deleteAll();
    } catch (e) {
      throw SecureStorageException('Platform secure storage deleteAll failed', e);
    }
  }

  @override
  Future<Map<String, String>> readAll() async {
    try {
      return await _storage.readAll();
    } catch (e) {
      throw SecureStorageException('Platform secure storage readAll failed', e);
    }
  }
}

class SecureStorageService {
  static final SecureStorageService instance = SecureStorageService();

  static const String keyMachineId = 'active_machine_id';
  static const String keyDeviceId = 'active_device_id';
  static const String keySessionId = 'active_session_id';
  static const String keyToken = 'active_client_token';
  static const String keyMasterSecret = 'active_master_secret';
  static const String keyBrokerUrl = 'active_broker_url';
  static const String keyCommitSignature = 'active_session_commit_sig';

  final SecureStorageDriver _driver;

  SecureStorageService({SecureStorageDriver? driver})
      : _driver = driver ?? PlatformSecureStorageDriver();

  static String _calculateCommitSignature({
    required String machineId,
    required String deviceId,
    required String sessionId,
    required String token,
    required String masterSecret,
    String? brokerUrl,
  }) {
    final raw = '$machineId|$deviceId|$sessionId|$token|$masterSecret|${brokerUrl ?? ""}';
    return sha256.convert(utf8.encode(raw)).toString();
  }

  Future<void> write(String key, String value) async {
    await _driver.write(key: key, value: value);
  }

  Future<String?> read(String key) async {
    return await _driver.read(key: key);
  }

  Future<void> delete(String key) async {
    await _driver.delete(key: key);
  }

  /// Atomically persists paired session credentials.
  /// If any write fails, any partial state is rolled back and an exception is thrown.
  Future<void> savePairedSession({
    required String machineId,
    required String deviceId,
    required String sessionId,
    required String token,
    required String masterSecret,
    String? brokerUrl,
  }) async {
    if (machineId.isEmpty ||
        deviceId.isEmpty ||
        sessionId.isEmpty ||
        token.isEmpty ||
        masterSecret.isEmpty) {
      throw SecureStorageException('Cannot save session with empty required credentials');
    }

    final commitSig = _calculateCommitSignature(
      machineId: machineId,
      deviceId: deviceId,
      sessionId: sessionId,
      token: token,
      masterSecret: masterSecret,
      brokerUrl: brokerUrl,
    );

    try {
      // 1. Invalidate any existing commit signature first to prevent partial write confusion
      await _driver.delete(key: keyCommitSignature);

      // 2. Write sensitive session credentials
      await _driver.write(key: keyMachineId, value: machineId);
      await _driver.write(key: keyDeviceId, value: deviceId);
      await _driver.write(key: keySessionId, value: sessionId);
      await _driver.write(key: keyToken, value: token);
      await _driver.write(key: keyMasterSecret, value: masterSecret);
      if (brokerUrl != null && brokerUrl.isNotEmpty) {
        await _driver.write(key: keyBrokerUrl, value: brokerUrl);
      } else {
        await _driver.delete(key: keyBrokerUrl);
      }

      // 3. Atomically finalize by writing verified commit signature
      await _driver.write(key: keyCommitSignature, value: commitSig);
    } catch (e) {
      // Rollback on partial write failure: fail closed
      try {
        await clearPairedSession();
      } catch (_) {}
      throw SecureStorageException('Failed to persist paired session securely; session cleared', e);
    }
  }

  /// Loads and verifies paired session credentials.
  /// Rejects partial writes, missing fields, or signature mismatch, failing closed.
  Future<Map<String, String>?> getPairedSession() async {
    try {
      final commitSig = await _driver.read(key: keyCommitSignature);
      if (commitSig == null || commitSig.isEmpty) {
        // No valid commit signature: uninitialized or partial write.
        return null;
      }

      final machineId = await _driver.read(key: keyMachineId);
      final deviceId = await _driver.read(key: keyDeviceId);
      final sessionId = await _driver.read(key: keySessionId);
      final token = await _driver.read(key: keyToken);
      final masterSecret = await _driver.read(key: keyMasterSecret);
      final brokerUrl = await _driver.read(key: keyBrokerUrl);

      if (machineId == null ||
          deviceId == null ||
          sessionId == null ||
          token == null ||
          masterSecret == null ||
          machineId.isEmpty ||
          deviceId.isEmpty ||
          sessionId.isEmpty ||
          token.isEmpty ||
          masterSecret.isEmpty) {
        // Corrupted or missing field: clear invalid state and return null
        await clearPairedSession();
        return null;
      }

      final expectedSig = _calculateCommitSignature(
        machineId: machineId,
        deviceId: deviceId,
        sessionId: sessionId,
        token: token,
        masterSecret: masterSecret,
        brokerUrl: brokerUrl,
      );

      if (commitSig != expectedSig) {
        // Integrity check failed! Corrupted or tampered session state.
        await clearPairedSession();
        return null;
      }

      return {
        'machineId': machineId,
        'deviceId': deviceId,
        'sessionId': sessionId,
        'token': token,
        'masterSecret': masterSecret,
        if (brokerUrl != null && brokerUrl.isNotEmpty) 'brokerUrl': brokerUrl,
      };
    } catch (e) {
      throw SecureStorageException('Failed to read secure session credentials', e);
    }
  }

  Future<void> clearPairedSession() async {
    try {
      await _driver.delete(key: keyCommitSignature);
      await _driver.delete(key: keyMachineId);
      await _driver.delete(key: keyDeviceId);
      await _driver.delete(key: keySessionId);
      await _driver.delete(key: keyToken);
      await _driver.delete(key: keyMasterSecret);
      await _driver.delete(key: keyBrokerUrl);
    } catch (e) {
      throw SecureStorageException('Failed to clear secure session credentials', e);
    }
  }

  String generateDeviceProof(String challengePayload, String deviceId, String secretKey) {
    final key = utf8.encode(secretKey);
    final bytes = utf8.encode('$challengePayload:$deviceId');
    final hmacSha256 = Hmac(sha256, key);
    final digest = hmacSha256.convert(bytes);
    return digest.toString();
  }
}

import 'dart:convert';
import 'package:crypto/crypto.dart';

class SecureStorageService {
  static final SecureStorageService instance = SecureStorageService();

  static const String keyMachineId = 'active_machine_id';
  static const String keyDeviceId = 'active_device_id';
  static const String keySessionId = 'active_session_id';
  static const String keyToken = 'active_client_token';
  static const String keyMasterSecret = 'active_master_secret';
  static const String keyBrokerUrl = 'active_broker_url';

  final Map<String, String> _memoryStore = {};

  Future<void> write(String key, String value) async {
    _memoryStore[key] = value;
  }

  Future<String?> read(String key) async {
    return _memoryStore[key];
  }

  Future<void> delete(String key) async {
    _memoryStore.remove(key);
  }

  Future<void> savePairedSession({
    required String machineId,
    required String deviceId,
    required String sessionId,
    required String token,
    required String masterSecret,
    String? brokerUrl,
  }) async {
    await write(keyMachineId, machineId);
    await write(keyDeviceId, deviceId);
    await write(keySessionId, sessionId);
    await write(keyToken, token);
    await write(keyMasterSecret, masterSecret);
    if (brokerUrl != null && brokerUrl.isNotEmpty) {
      await write(keyBrokerUrl, brokerUrl);
    }
  }

  Future<Map<String, String>?> getPairedSession() async {
    final machineId = await read(keyMachineId);
    final deviceId = await read(keyDeviceId);
    final sessionId = await read(keySessionId);
    final token = await read(keyToken);
    final masterSecret = await read(keyMasterSecret);

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
      return null;
    }

    final brokerUrl = await read(keyBrokerUrl);
    return {
      'machineId': machineId,
      'deviceId': deviceId,
      'sessionId': sessionId,
      'token': token,
      'masterSecret': masterSecret,
      if (brokerUrl != null) 'brokerUrl': brokerUrl,
    };
  }

  Future<void> clearPairedSession() async {
    await delete(keyMachineId);
    await delete(keyDeviceId);
    await delete(keySessionId);
    await delete(keyToken);
    await delete(keyMasterSecret);
    await delete(keyBrokerUrl);
  }

  String generateDeviceProof(String challengePayload, String deviceId, String secretKey) {
    final key = utf8.encode(secretKey);
    final bytes = utf8.encode('$challengePayload:$deviceId');
    final hmacSha256 = Hmac(sha256, key);
    final digest = hmacSha256.convert(bytes);
    return digest.toString();
  }
}

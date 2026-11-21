import 'dart:convert';
import 'package:crypto/crypto.dart';

class SecureStorageService {
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

  String generateDeviceProof(String challengePayload, String deviceId, String secretKey) {
    final key = utf8.encode(secretKey);
    final bytes = utf8.encode('$challengePayload:$deviceId');
    final hmacSha256 = Hmac(sha256, key);
    final digest = hmacSha256.convert(bytes);
    return digest.toString();
  }
}

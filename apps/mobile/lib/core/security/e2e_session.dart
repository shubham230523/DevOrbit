import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';

enum Direction {
  clientToHost,
  hostToClient,
}

class FlutterE2ESession {
  final String sessionId;
  final String deviceId;
  final SecretKey clientToHostKey;
  final SecretKey hostToClientKey;
  final AesGcm _aesGcm = AesGcm.with256bits();

  FlutterE2ESession({
    required this.sessionId,
    required this.deviceId,
    required this.clientToHostKey,
    required this.hostToClientKey,
  });

  static Future<FlutterE2ESession> create({
    required String sessionId,
    required String deviceId,
    required String masterSecret,
  }) async {
    final hkdf = Hkdf(
      hmac: Hmac.sha256(),
      outputLength: 32,
    );

    final secretBytes = utf8.encode(masterSecret);
    final salt = utf8.encode('devorbit-e2e-salt-$sessionId');
    final infoClient = utf8.encode('devorbit-client-to-host-$sessionId-$deviceId');
    final infoHost = utf8.encode('devorbit-host-to-client-$sessionId-$deviceId');

    final clientKeyBytes = await hkdf.deriveKey(
      secretKey: SecretKey(secretBytes),
      nonce: salt,
      info: infoClient,
    );

    final hostKeyBytes = await hkdf.deriveKey(
      secretKey: SecretKey(secretBytes),
      nonce: salt,
      info: infoHost,
    );

    return FlutterE2ESession(
      sessionId: sessionId,
      deviceId: deviceId,
      clientToHostKey: clientKeyBytes,
      hostToClientKey: hostKeyBytes,
    );
  }

  List<int> constructAAD(int seqNum, int timestampMicroseconds) {
    final builder = BytesBuilder();
    builder.add(utf8.encode(sessionId));
    builder.addByte(0x3A); // ':'
    builder.add(utf8.encode(deviceId));
    builder.addByte(0x3A); // ':'

    final seqData = ByteData(8)..setUint64(0, seqNum, Endian.big);
    builder.add(seqData.buffer.asUint8List());

    final tsData = ByteData(8)..setUint64(0, timestampMicroseconds, Endian.big);
    builder.add(tsData.buffer.asUint8List());

    return builder.toBytes();
  }

  Future<String> encrypt({
    required Direction direction,
    required String plainText,
    required int seqNum,
    required int timestamp,
  }) async {
    final key = direction == Direction.clientToHost ? clientToHostKey : hostToClientKey;

    final random = Random.secure();
    final nonce = Uint8List.fromList(List<int>.generate(12, (_) => random.nextInt(256)));
    final aad = constructAAD(seqNum, timestamp);

    final secretBox = await _aesGcm.encrypt(
      utf8.encode(plainText),
      secretKey: key,
      nonce: nonce,
      aad: aad,
    );

    final combined = BytesBuilder();
    combined.add(secretBox.nonce);
    combined.add(secretBox.cipherText);
    combined.add(secretBox.mac.bytes);

    return combined.toBytes().map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  }

  Future<String> decrypt({
    required Direction direction,
    required String cipherTextHex,
    required int seqNum,
    required int timestamp,
  }) async {
    final key = direction == Direction.clientToHost ? clientToHostKey : hostToClientKey;

    final bytes = Uint8List(cipherTextHex.length ~/ 2);
    for (var i = 0; i < bytes.length; i++) {
      bytes[i] = int.parse(cipherTextHex.substring(i * 2, i * 2 + 2), radix: 16);
    }

    if (bytes.length < 28) {
      throw const FormatException('Ciphertext too short');
    }

    final nonce = bytes.sublist(0, 12);
    final cipherText = bytes.sublist(12, bytes.length - 16);
    final mac = Mac(bytes.sublist(bytes.length - 16));
    final aad = constructAAD(seqNum, timestamp);

    final secretBox = SecretBox(
      cipherText,
      nonce: nonce,
      mac: mac,
    );

    final decryptedBytes = await _aesGcm.decrypt(
      secretBox,
      secretKey: key,
      aad: aad,
    );

    return utf8.decode(decryptedBytes);
  }
}

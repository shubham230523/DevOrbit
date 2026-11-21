import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/core/security/e2e_session.dart';

void main() {
  test('FlutterE2ESession encrypts and decrypts with directional keys and AAD', () async {
    final session = await FlutterE2ESession.create(
      sessionId: 'sess-test-01',
      deviceId: 'dev-phone-01',
      masterSecret: 'devorbit-master-secret-key-32b!',
    );

    const plainText = '{"v":"1.2","request_id":"req_1","method":"GET","path":"/api/v1/health"}';
    const seqNum = 1;
    final timestamp = DateTime.now().microsecondsSinceEpoch;

    // Encrypt client-to-host
    final encryptedHex = await session.encrypt(
      direction: Direction.clientToHost,
      plainText: plainText,
      seqNum: seqNum,
      timestamp: timestamp,
    );

    expect(encryptedHex.length, greaterThan(plainText.length));

    // Decrypt client-to-host
    final decryptedText = await session.decrypt(
      direction: Direction.clientToHost,
      cipherTextHex: encryptedHex,
      seqNum: seqNum,
      timestamp: timestamp,
    );

    expect(decryptedText, equals(plainText));
  });
}

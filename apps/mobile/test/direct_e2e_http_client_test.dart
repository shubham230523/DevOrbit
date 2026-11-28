import 'dart:convert';
import 'package:crypto/crypto.dart' as crypto;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:devorbit_mobile/core/network/agent_api_client.dart';
import 'package:devorbit_mobile/core/network/api_client.dart';
import 'package:devorbit_mobile/core/network/direct_e2e_http_client.dart';
import 'package:devorbit_mobile/core/security/e2e_session.dart';

void main() {
  test('DirectE2EHttpTransport.resolveHostBridgeHttpUrl maps ws/wss URLs to http/https', () {
    expect(DirectE2EHttpTransport.resolveHostBridgeHttpUrl(null), 'http://localhost:8082');
    expect(DirectE2EHttpTransport.resolveHostBridgeHttpUrl(''), 'http://localhost:8082');
    expect(DirectE2EHttpTransport.resolveHostBridgeHttpUrl('ws://192.168.1.42:8081'), 'http://192.168.1.42:8082');
    expect(DirectE2EHttpTransport.resolveHostBridgeHttpUrl('wss://broker.devorbit.net:8081'), 'https://broker.devorbit.net:8082');
    expect(DirectE2EHttpTransport.resolveHostBridgeHttpUrl('http://10.0.0.5:9090/some/path'), 'http://10.0.0.5:9090');
  });

  test('HostBridgeApiClient and AgentApiClient fail closed when E2E credentials are missing', () async {
    final apiClient = HostBridgeApiClient(baseUrl: 'http://127.0.0.1:8082', sessionToken: 'only-token');
    final wsRes = await apiClient.listWorkspaces();
    expect(wsRes['success'], false);
    expect(wsRes['error'], contains('Authenticated E2E session credentials required'));

    final agentClient = AgentApiClient(baseUrl: 'http://127.0.0.1:8082', sessionToken: 'only-token');
    final taskRes = await agentClient.createAgentTask(workspaceId: '/tmp/ws', prompt: 'hi');
    expect(taskRes['success'], false);
    expect(taskRes['error'], contains('Authenticated E2E session credentials required'));
  });

  test('DirectE2EHttpTransport encrypts request on http channel and decrypts host response', () async {
    const sessId = 'sess-dart-e2e-01';
    const devId = 'dev-dart-e2e-01';
    const token = 'tok-dart-e2e-secret-value-01!';
    const secret = 'master-dart-e2e-secret-32bytes-01!';

    final serverE2E = await FlutterE2ESession.create(
      sessionId: sessId,
      deviceId: devId,
      masterSecret: secret,
      channel: 'http',
    );
    final serverAuthKey = await FlutterE2ESession.deriveHttpAuthKey(
      sessionId: sessId,
      deviceId: devId,
      masterSecret: secret,
    );

    final mockHttp = MockClient((http.Request req) async {
      expect(req.method, 'POST');
      expect(req.url.path, '/api/v1/e2e');
      expect(req.headers.containsKey('Authorization'), isFalse);
      expect(req.headers['X-DevOrbit-Session-ID'], sessId);
      expect(req.headers['X-DevOrbit-Device-ID'], devId);
      final reqTs = req.headers['X-DevOrbit-Timestamp'] ?? '';
      final reqNonce = req.headers['X-DevOrbit-Nonce'] ?? '';
      final reqSig = req.headers['X-DevOrbit-Signature'] ?? '';
      expect(reqTs, isNotEmpty);
      expect(reqNonce.length, greaterThanOrEqualTo(16));
      final bodyHash = crypto.sha256.convert(utf8.encode(req.body)).toString();
      final expectedSig = FlutterE2ESession.computeHttpRequestMac(
        authKey: serverAuthKey,
        method: req.method,
        requestUri: req.url.path,
        sessionId: sessId,
        deviceId: devId,
        timestamp: reqTs,
        nonce: reqNonce,
        bodySha256Hex: bodyHash,
      );
      expect(reqSig, equals(expectedSig));

      final outer = jsonDecode(req.body) as Map<String, dynamic>;
      expect(outer['session_id'], sessId);
      expect(outer['type'], 'data');
      final seqNum = outer['seq_num'] as int;
      final ts = outer['timestamp'] as int;

      final plainReqStr = await serverE2E.decrypt(
        direction: Direction.clientToHost,
        cipherTextHex: outer['encrypted_payload'] as String,
        seqNum: seqNum,
        timestamp: ts,
      );
      final appReq = jsonDecode(plainReqStr) as Map<String, dynamic>;
      expect(appReq['method'], 'GET');
      expect(appReq['path'], '/api/v1/workspaces');

      final respTs = DateTime.now().microsecondsSinceEpoch;
      final plainRespStr = jsonEncode({
        'v': '1.2',
        'session_id': sessId,
        'request_id': appReq['request_id'],
        'status_code': 200,
        'headers': {'Content-Type': 'application/json'},
        'body': jsonEncode([
          {'id': 'ws-1', 'name': 'DevOrbit', 'root_path': '/work/DevOrbit'}
        ]),
        'seq_num': seqNum,
        'timestamp': respTs,
      });
      final encResp = await serverE2E.encrypt(
        direction: Direction.hostToClient,
        plainText: plainRespStr,
        seqNum: seqNum,
        timestamp: respTs,
      );

      return http.Response(
        jsonEncode({
          'v': '1.2',
          'session_id': sessId,
          'type': 'data',
          'encrypted_payload': encResp,
          'seq_num': seqNum,
          'timestamp': respTs,
        }),
        200,
        headers: {'content-type': 'application/json'},
      );
    });

    final client = HostBridgeApiClient(
      baseUrl: 'http://127.0.0.1:8082',
      sessionToken: token,
      sessionId: sessId,
      deviceId: devId,
      masterSecret: secret,
      httpClient: mockHttp,
    );

    final res = await client.listWorkspaces();
    expect(res['success'], true);
    expect((res['workspaces'] as List).length, 1);
    expect((res['workspaces'] as List).first['name'], 'DevOrbit');
  });
}

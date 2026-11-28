import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;
import 'direct_e2e_http_client.dart';

class AgentApiClient {
  final String baseUrl;
  final String? sessionToken;
  final String? sessionId;
  final String? deviceId;
  final String? masterSecret;
  final DirectE2EHttpTransport _e2eTransport;

  AgentApiClient({
    required this.baseUrl,
    this.sessionToken,
    this.sessionId,
    this.deviceId,
    this.masterSecret,
    http.Client? httpClient,
  }) : _e2eTransport = DirectE2EHttpTransport(
          baseUrl: baseUrl,
          sessionToken: sessionToken,
          sessionId: sessionId,
          deviceId: deviceId,
          masterSecret: masterSecret,
          httpClient: httpClient,
        );

  Future<http.Response> _sendProtected({
    required String method,
    required String path,
    String? body,
    Duration timeout = const Duration(seconds: 10),
  }) async {
    if (!_e2eTransport.hasE2ECredentials) {
      throw StateError('Authenticated E2E session credentials required for direct Host Bridge agent API access');
    }
    return _e2eTransport.send(
      method: method,
      path: path,
      body: body,
      timeout: timeout,
    );
  }

  Future<Map<String, dynamic>> createAgentTask({
    required String workspaceId,
    required String prompt,
    String modelId = 'gemini-2.5-flash',
  }) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/agent/tasks',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'prompt': prompt,
          'model_id': modelId,
        }),
        timeout: const Duration(seconds: 10),
      );

      if (res.statusCode == 200) {
        return {'success': true, 'task': jsonDecode(res.body)};
      } else {
        return {'success': false, 'error': 'HTTP ${res.statusCode}: ${res.body}'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>> getAgentTask(String taskId) async {
    try {
      final res = await _sendProtected(
        method: 'GET',
        path: '/api/v1/agent/tasks/$taskId',
        timeout: const Duration(seconds: 5),
      );

      if (res.statusCode == 200) {
        return {'success': true, 'task': jsonDecode(res.body)};
      } else {
        return {'success': false, 'error': 'HTTP ${res.statusCode}: ${res.body}'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>> approveAgentTask({
    required String taskId,
    required String approvalToken,
  }) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/agent/tasks/approve',
        body: jsonEncode({
          'task_id': taskId,
          'approval_token': approvalToken,
        }),
        timeout: const Duration(seconds: 10),
      );

      if (res.statusCode == 200) {
        return {'success': true, 'data': jsonDecode(res.body)};
      } else {
        return {'success': false, 'error': 'HTTP ${res.statusCode}: ${res.body}'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>> cancelAgentTask(String taskId) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/agent/tasks/cancel',
        body: jsonEncode({
          'task_id': taskId,
        }),
        timeout: const Duration(seconds: 5),
      );

      if (res.statusCode == 200) {
        return {'success': true, 'data': jsonDecode(res.body)};
      } else {
        return {'success': false, 'error': 'HTTP ${res.statusCode}: ${res.body}'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }
}

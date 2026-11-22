import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;

class AgentApiClient {
  final String baseUrl;
  final String? sessionToken;

  AgentApiClient({required this.baseUrl, this.sessionToken});

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (sessionToken != null) 'Authorization': 'Bearer $sessionToken',
      };

  Future<Map<String, dynamic>> createAgentTask({
    required String workspaceId,
    required String prompt,
    String modelId = 'gemini-2.5-flash',
  }) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/agent/tasks'),
        headers: _headers,
        body: jsonEncode({
          'workspace_id': workspaceId,
          'prompt': prompt,
          'model_id': modelId,
        }),
      ).timeout(const Duration(seconds: 10));

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
      final res = await http.get(
        Uri.parse('$baseUrl/api/v1/agent/tasks/$taskId'),
        headers: _headers,
      ).timeout(const Duration(seconds: 5));

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
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/agent/tasks/approve'),
        headers: _headers,
        body: jsonEncode({
          'task_id': taskId,
          'approval_token': approvalToken,
        }),
      ).timeout(const Duration(seconds: 10));

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
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/agent/tasks/cancel'),
        headers: _headers,
        body: jsonEncode({
          'task_id': taskId,
        }),
      ).timeout(const Duration(seconds: 5));

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

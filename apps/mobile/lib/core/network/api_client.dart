import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;

class HostBridgeApiClient {
  final String baseUrl;
  final String? sessionToken;

  HostBridgeApiClient({required this.baseUrl, this.sessionToken});

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (sessionToken != null) 'Authorization': 'Bearer $sessionToken',
      };

  Future<Map<String, dynamic>?> getHealth() async {
    try {
      final stopwatch = Stopwatch()..start();
      final res = await http.get(
        Uri.parse('$baseUrl/api/v1/health'),
        headers: _headers,
      ).timeout(const Duration(seconds: 2));
      stopwatch.stop();

      if (res.statusCode == 200) {
        final data = jsonDecode(res.body) as Map<String, dynamic>;
        data['latency_ms'] = stopwatch.elapsedMilliseconds;
        return data;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>> listWorkspaces() async {
    try {
      final res = await http.get(
        Uri.parse('$baseUrl/api/v1/workspaces'),
        headers: _headers,
      ).timeout(const Duration(milliseconds: 2500));

      if (res.statusCode == 200) {
        final list = jsonDecode(res.body) as List<dynamic>;
        return {'success': true, 'workspaces': list};
      } else {
        return {'success': false, 'error': 'HTTP ${res.statusCode}: ${res.body}'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>?> listDirectory(String workspaceId, String relativePath) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/files/list'),
        headers: _headers,
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
        }),
      ).timeout(const Duration(milliseconds: 2500));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>?> readFile(String workspaceId, String relativePath) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/files/read'),
        headers: _headers,
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
        }),
      ).timeout(const Duration(seconds: 8));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>> writeFile({
    required String workspaceId,
    required String relativePath,
    required String newContent,
    required String baseSHA256,
    String? requestId,
  }) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/files/write'),
        headers: _headers,
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
          'new_content': newContent,
          'base_sha256': baseSHA256,
          if (requestId != null) 'request_id': requestId,
        }),
      ).timeout(const Duration(seconds: 8));

      final body = jsonDecode(res.body) as Map<String, dynamic>;
      if (res.statusCode == 200) {
        return {'success': true, 'data': body};
      } else if (res.statusCode == 409) {
        return {'success': false, 'conflict': true, 'data': body};
      } else {
        return {'success': false, 'error': body['error'] ?? 'Write failed'};
      }
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>?> getGitStatus(String workspaceId) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/git/status'),
        headers: _headers,
        body: jsonEncode({'workspace_id': workspaceId}),
      ).timeout(const Duration(seconds: 5));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>?> getGitDiff(String workspaceId) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/git/diff'),
        headers: _headers,
        body: jsonEncode({'workspace_id': workspaceId}),
      ).timeout(const Duration(seconds: 5));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>?> executeProcess({
    required String workspaceId,
    required String command,
    required List<String> args,
    String? requestId,
  }) async {
    try {
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/processes/execute'),
        headers: _headers,
        body: jsonEncode({
          'workspace_id': workspaceId,
          'command': command,
          'args': args,
          if (requestId != null) 'request_id': requestId,
        }),
      ).timeout(const Duration(seconds: 10));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<List<dynamic>> listADBDevices() async {
    try {
      final res = await http.get(
        Uri.parse('$baseUrl/api/v1/devices/adb'),
        headers: _headers,
      ).timeout(const Duration(seconds: 5));
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as List<dynamic>;
      }
    } catch (_) {}
    return [];
  }

  Future<Map<String, dynamic>> listGitHubRepos({String? githubToken}) async {
    try {
      final headers = {
        ..._headers,
        if (githubToken != null && githubToken.trim().isNotEmpty)
          'X-GitHub-Token': githubToken.trim(),
      };
      final res = await http.get(
        Uri.parse('$baseUrl/api/v1/github/repos'),
        headers: headers,
      ).timeout(const Duration(seconds: 15));

      final body = jsonDecode(res.body) as Map<String, dynamic>;
      if (res.statusCode == 200) {
        return {
          'success': true,
          'repositories': body['repositories'] ?? <dynamic>[],
          'clone_base': body['clone_base'],
        };
      }
      return {
        'success': false,
        'error': body['error'] ?? 'HTTP ${res.statusCode}',
      };
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }

  Future<Map<String, dynamic>> cloneGitHubRepo({
    required String cloneUrl,
    required String name,
    String? githubToken,
  }) async {
    try {
      final headers = {
        ..._headers,
        if (githubToken != null && githubToken.trim().isNotEmpty)
          'X-GitHub-Token': githubToken.trim(),
      };
      final res = await http.post(
        Uri.parse('$baseUrl/api/v1/github/clone'),
        headers: headers,
        body: jsonEncode({
          'clone_url': cloneUrl,
          'name': name,
        }),
      ).timeout(const Duration(seconds: 60));

      final body = jsonDecode(res.body) as Map<String, dynamic>;
      if (res.statusCode == 200) {
        return {'success': true, 'data': body};
      }
      return {
        'success': false,
        'error': body['error'] ?? 'HTTP ${res.statusCode}',
      };
    } catch (e) {
      return {'success': false, 'error': e.toString()};
    }
  }
}

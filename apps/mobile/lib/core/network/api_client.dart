import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;
import 'direct_e2e_http_client.dart';
import '../../features/tasks/application/process_approval_controller.dart';

/// Adapter that lets [ProcessApprovalController] drive the real Host Bridge client, so the approval
/// state machine is shared by production code and its tests.
class HostBridgeProcessApprovalApi implements ProcessApprovalApi {
  HostBridgeProcessApprovalApi(this._client);

  final HostBridgeApiClient _client;

  @override
  Future<ProcessApprovalRequestResult> requestProcessApproval({
    required String workspaceId,
    required String command,
    required List<String> args,
  }) =>
      _client.requestProcessApproval(workspaceId: workspaceId, command: command, args: args);

  @override
  Future<ProcessApprovalDecisionResult> decideProcessApproval({
    required String approvalId,
    required String decision,
    required String commandDigest,
  }) =>
      _client.decideProcessApproval(
        approvalId: approvalId,
        decision: decision,
        commandDigest: commandDigest,
      );

  @override
  Future<ProcessExecutionResult> executeProcess({
    required String workspaceId,
    required String command,
    required List<String> args,
    String? approvalToken,
  }) async {
    final result = await _client.executeProcess(
      workspaceId: workspaceId,
      command: command,
      args: args,
      approvalToken: approvalToken,
    );
    if (result != null) {
      return ProcessExecutionResult(statusCode: 200, process: result);
    }
    return const ProcessExecutionResult(statusCode: 403, error: 'Execution rejected by the host');
  }
}

class HostBridgeApiClient {
  final String baseUrl;
  final String? sessionToken;
  final String? sessionId;
  final String? deviceId;
  final String? masterSecret;
  final http.Client _httpClient;
  final DirectE2EHttpTransport _e2eTransport;

  HostBridgeApiClient({
    required this.baseUrl,
    this.sessionToken,
    this.sessionId,
    this.deviceId,
    this.masterSecret,
    http.Client? httpClient,
  })  : _httpClient = httpClient ?? http.Client(),
        _e2eTransport = DirectE2EHttpTransport(
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
    Map<String, String>? headers,
    String? body,
    Duration timeout = const Duration(seconds: 10),
  }) async {
    if (!_e2eTransport.hasE2ECredentials) {
      throw StateError('Authenticated E2E session credentials required for direct Host Bridge API access');
    }
    return _e2eTransport.send(
      method: method,
      path: path,
      headers: headers,
      body: body,
      timeout: timeout,
    );
  }

  Future<Map<String, dynamic>?> getHealth() async {
    try {
      final stopwatch = Stopwatch()..start();
      final http.Response res;
      if (_e2eTransport.hasE2ECredentials) {
        res = await _e2eTransport.send(
          method: 'GET',
          path: '/api/v1/health',
          timeout: const Duration(seconds: 2),
        );
      } else {
        res = await _httpClient
            .get(Uri.parse('$baseUrl/api/v1/health'))
            .timeout(const Duration(seconds: 2));
      }
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
      final res = await _sendProtected(
        method: 'GET',
        path: '/api/v1/workspaces',
        timeout: const Duration(milliseconds: 2500),
      );

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
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/files/list',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
        }),
        timeout: const Duration(milliseconds: 2500),
      );
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>?> readFile(String workspaceId, String relativePath) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/files/read',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
        }),
        timeout: const Duration(seconds: 8),
      );
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
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/files/write',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'relative_path': relativePath,
          'new_content': newContent,
          'base_sha256': baseSHA256,
          if (requestId != null) 'request_id': requestId,
        }),
        timeout: const Duration(seconds: 8),
      );

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
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/git/status',
        body: jsonEncode({'workspace_id': workspaceId}),
        timeout: const Duration(seconds: 5),
      );
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  Future<Map<String, dynamic>?> getGitDiff(String workspaceId) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/git/diff',
        body: jsonEncode({'workspace_id': workspaceId}),
        timeout: const Duration(seconds: 5),
      );
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
    String? approvalToken,
  }) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/processes/execute',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'command': command,
          'args': args,
          if (requestId != null) 'request_id': requestId,
          if (approvalToken != null) 'approval_token': approvalToken,
        }),
        timeout: const Duration(seconds: 10),
      );
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as Map<String, dynamic>;
      }
    } catch (_) {}
    return null;
  }

  /// Requests a process approval. Returns the raw status so the caller can distinguish 202 (pending),
  /// 400 (command needs no approval) and 403 (not permitted).
  Future<ProcessApprovalRequestResult> requestProcessApproval({
    required String workspaceId,
    required String command,
    required List<String> args,
  }) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/processes/approvals',
        body: jsonEncode({
          'workspace_id': workspaceId,
          'command': command,
          'args': args,
        }),
        timeout: const Duration(seconds: 10),
      );
      if (res.statusCode == 202) {
        final body = jsonDecode(res.body) as Map<String, dynamic>;
        return ProcessApprovalRequestResult(
          statusCode: 202,
          proposal: ProcessApprovalProposal.fromJson(body),
        );
      }
      return ProcessApprovalRequestResult(
        statusCode: res.statusCode,
        error: _errorMessage(res.body),
      );
    } catch (e) {
      return ProcessApprovalRequestResult(statusCode: 0, error: e.toString());
    }
  }

  /// Submits the human decision. A rejection needs no digest; an approval must echo the digest of the
  /// proposal the user was shown.
  Future<ProcessApprovalDecisionResult> decideProcessApproval({
    required String approvalId,
    required String decision,
    required String commandDigest,
  }) async {
    try {
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/processes/approvals/$approvalId/decision',
        body: jsonEncode({
          'decision': decision,
          if (commandDigest.isNotEmpty) 'command_digest': commandDigest,
        }),
        timeout: const Duration(seconds: 10),
      );
      if (res.statusCode == 200) {
        final body = jsonDecode(res.body) as Map<String, dynamic>;
        return ProcessApprovalDecisionResult(
          statusCode: 200,
          state: body['state'] as String?,
          executionToken: body['execution_token'] as String?,
        );
      }
      return ProcessApprovalDecisionResult(
        statusCode: res.statusCode,
        error: _errorMessage(res.body),
      );
    } catch (e) {
      return ProcessApprovalDecisionResult(statusCode: 0, error: e.toString());
    }
  }

  String _errorMessage(String body) {
    try {
      final decoded = jsonDecode(body);
      if (decoded is Map<String, dynamic> && decoded['error'] is String) {
        return decoded['error'] as String;
      }
    } catch (_) {}
    return 'Request failed';
  }

  Future<List<dynamic>> listADBDevices() async {
    try {
      final res = await _sendProtected(
        method: 'GET',
        path: '/api/v1/devices/adb',
        timeout: const Duration(seconds: 5),
      );
      if (res.statusCode == 200) {
        return jsonDecode(res.body) as List<dynamic>;
      }
    } catch (_) {}
    return [];
  }

  Future<Map<String, dynamic>> listGitHubRepos({String? githubToken}) async {
    try {
      final headers = <String, String>{
        if (githubToken != null && githubToken.trim().isNotEmpty)
          'X-GitHub-Token': githubToken.trim(),
      };
      final res = await _sendProtected(
        method: 'GET',
        path: '/api/v1/github/repos',
        headers: headers,
        timeout: const Duration(seconds: 15),
      );

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
      final headers = <String, String>{
        if (githubToken != null && githubToken.trim().isNotEmpty)
          'X-GitHub-Token': githubToken.trim(),
      };
      final res = await _sendProtected(
        method: 'POST',
        path: '/api/v1/github/clone',
        headers: headers,
        body: jsonEncode({
          'clone_url': cloneUrl,
          'name': name,
        }),
        timeout: const Duration(seconds: 60),
      );

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

import 'dart:convert';

import '../../../core/network/agent_api_client.dart';
import '../../tasks/application/process_approval_controller.dart';

/// Adapter that lets the shared [ProcessApprovalController] drive the agent task approval flow.
///
/// Only the operations the agent flow needs are implemented: fetch the host-issued proposal, submit
/// the decision, and cancel. Requesting a new proposal and executing a command directly are not part
/// of this flow — the host creates the proposal when the AI requests the command, and it consumes the
/// authorization when the task resumes.
class AgentProcessApprovalApi implements ProcessApprovalApi {
  AgentProcessApprovalApi(this._client);

  final AgentApiClient _client;

  @override
  Future<ProcessApprovalFetchResult> fetchProcessApproval({required String approvalId}) async {
    try {
      final res = await _client.getProcessApproval(approvalId);
      if (res.statusCode == 200) {
        return ProcessApprovalFetchResult(
          statusCode: 200,
          proposal: ProcessApprovalProposal.fromJson(
            jsonDecode(res.body) as Map<String, dynamic>,
          ),
        );
      }
      return ProcessApprovalFetchResult(
        statusCode: res.statusCode,
        error: _message(res.body),
      );
    } catch (e) {
      return ProcessApprovalFetchResult(statusCode: 0, error: e.toString());
    }
  }

  @override
  Future<ProcessApprovalDecisionResult> decideProcessApproval({
    required String approvalId,
    required String decision,
    required String commandDigest,
  }) async {
    try {
      final res = await _client.decideProcessApproval(
        approvalId: approvalId,
        decision: decision,
        commandDigest: commandDigest,
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
        error: _message(res.body),
      );
    } catch (e) {
      return ProcessApprovalDecisionResult(statusCode: 0, error: e.toString());
    }
  }

  @override
  Future<bool> cancelProcessApproval({required String approvalId}) async {
    try {
      final res = await _client.cancelProcessApproval(approvalId);
      if (res.statusCode != 200) return false;
      final body = jsonDecode(res.body);
      return body is Map<String, dynamic> && body['state'] == 'cancelled';
    } catch (_) {
      return false;
    }
  }

  @override
  Future<ProcessApprovalRequestResult> requestProcessApproval({
    required String workspaceId,
    required String command,
    required List<String> args,
  }) async {
    // The host creates agent proposals; the client must never mint one for a task.
    return const ProcessApprovalRequestResult(
      statusCode: 400,
      error: 'Agent tasks cannot request a process approval from the client',
    );
  }

  @override
  Future<ProcessExecutionResult> executeProcess({
    required String workspaceId,
    required String command,
    required List<String> args,
    String? approvalToken,
  }) async {
    // Agent execution happens when the task resumes on the host, never from this path.
    return const ProcessExecutionResult(
      statusCode: 400,
      error: 'Agent task execution is performed by the host when the task resumes',
    );
  }

  String _message(String body) {
    try {
      final decoded = jsonDecode(body);
      if (decoded is Map<String, dynamic> && decoded['error'] is String) {
        return decoded['error'] as String;
      }
    } catch (_) {}
    return 'Request failed';
  }
}

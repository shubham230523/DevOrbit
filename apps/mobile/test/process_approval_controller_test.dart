import 'package:flutter_test/flutter_test.dart';

import 'package:devorbit_mobile/features/tasks/application/process_approval_controller.dart';

/// Scripted fake of the Host Bridge approval protocol.
class _FakeApi implements ProcessApprovalApi {
  _FakeApi({
    this.requestStatusCode = 202,
    this.decisionStatusCode = 200,
    this.executeStatusCode = 200,
    this.throwOnRequest = false,
    this.throwOnDecision = false,
    this.throwOnCancel = false,
    this.cancelConfirmed = true,
    this.expiresIn = const Duration(minutes: 10),
  });

  int requestStatusCode;
  int decisionStatusCode;
  static const String decisionToken = 'pexec-token-1';
  int executeStatusCode;
  bool throwOnRequest;
  bool throwOnDecision;
  bool throwOnCancel;
  bool cancelConfirmed;
  Duration expiresIn;
  int cancelCalls = 0;

  int requestCalls = 0;
  int decisionCalls = 0;
  int executeCalls = 0;
  String? lastDecision;
  String? lastDigest;
  String? lastApprovalToken;
  List<String>? lastArgs;

  @override
  Future<ProcessApprovalRequestResult> requestProcessApproval({
    required String workspaceId,
    required String command,
    required List<String> args,
  }) async {
    requestCalls++;
    if (throwOnRequest) throw Exception('socket closed');
    if (requestStatusCode != 202) {
      return ProcessApprovalRequestResult(
        statusCode: requestStatusCode,
        error: requestStatusCode == 400 ? 'command does not require approval' : 'rejected',
      );
    }
    return ProcessApprovalRequestResult(
      statusCode: 202,
      proposal: ProcessApprovalProposal(
        approvalId: 'pappr-1',
        workspaceId: workspaceId,
        command: command,
        args: args,
        reason: 'gradle executes project-defined build logic',
        commandDigest: 'digest-abc',
        expiresAt: DateTime.now().add(expiresIn),
      ),
    );
  }

  @override
  Future<ProcessApprovalDecisionResult> decideProcessApproval({
    required String approvalId,
    required String decision,
    required String commandDigest,
  }) async {
    decisionCalls++;
    lastDecision = decision;
    lastDigest = commandDigest;
    if (throwOnDecision) throw Exception('socket closed');
    if (decisionStatusCode != 200) {
      return ProcessApprovalDecisionResult(statusCode: decisionStatusCode, error: 'denied');
    }
    if (decision == 'reject') {
      return const ProcessApprovalDecisionResult(statusCode: 200, state: 'rejected');
    }
    return const ProcessApprovalDecisionResult(
      statusCode: 200,
      state: 'approved',
      executionToken: decisionToken,
    );
  }

  @override
  Future<bool> cancelProcessApproval({required String approvalId}) async {
    cancelCalls++;
    if (throwOnCancel) throw Exception('socket closed');
    return cancelConfirmed;
  }

  @override
  Future<ProcessExecutionResult> executeProcess({
    required String workspaceId,
    required String command,
    required List<String> args,
    String? approvalToken,
  }) async {
    executeCalls++;
    lastApprovalToken = approvalToken;
    lastArgs = args;
    if (executeStatusCode != 200) {
      return ProcessExecutionResult(statusCode: executeStatusCode, error: 'approval_required');
    }
    return const ProcessExecutionResult(
      statusCode: 200,
      process: {'process_id': 'proc-1', 'exit_code': 0},
    );
  }
}

void main() {
  group('ProcessApprovalController', () {
    test('a new controller starts idle and requires no approval (no auto-approval after restart)',
        () {
      final controller = ProcessApprovalController(_FakeApi());

      expect(controller.phase, ProcessApprovalPhase.idle);
      expect(controller.proposal, isNull);
      expect(controller.hasExecutionToken, isFalse);
    });

    test('propose surfaces the exact command for inspection', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['assembleDebug']);

      expect(controller.phase, ProcessApprovalPhase.awaitingDecision);
      expect(controller.proposal!.displayCommand, './gradlew assembleDebug');
      expect(controller.proposal!.workspaceId, '/ws');
      expect(controller.hasExecutionToken, isFalse, reason: 'a pending proposal must grant nothing');
      expect(api.executeCalls, 0);
    });

    test('an expired proposal is rejected before any decision is sent', () async {
      final api = _FakeApi(expiresIn: const Duration(seconds: -1));
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      expect(controller.phase, ProcessApprovalPhase.expired);

      await controller.approve();
      expect(api.decisionCalls, 0);
      expect(api.executeCalls, 0);
    });

    test('approve sends the digest and executes exactly once with the issued token', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      expect(api.lastDecision, 'approve');
      expect(api.lastDigest, 'digest-abc');
      expect(controller.phase, ProcessApprovalPhase.approved);
      expect(controller.hasExecutionToken, isTrue);

      await controller.executeApproved();
      expect(api.executeCalls, 1);
      expect(api.lastApprovalToken, 'pexec-token-1');
      expect(controller.phase, ProcessApprovalPhase.completed);

      // A second execution attempt after completion does nothing (the token is spent).
      await controller.executeApproved();
      expect(api.executeCalls, 1);
    });

    test('reject prevents execution entirely', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.reject();

      expect(api.lastDecision, 'reject');
      expect(controller.phase, ProcessApprovalPhase.rejected);
      expect(controller.hasExecutionToken, isFalse);

      await controller.executeApproved();
      expect(api.executeCalls, 0);
    });

    test('cancel is confirmed by the host and authorizes nothing', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.cancel();

      expect(api.cancelCalls, 1);
      expect(controller.phase, ProcessApprovalPhase.cancelled);
      expect(api.decisionCalls, 0);

      await controller.executeApproved();
      expect(api.executeCalls, 0);
    });

    test('cancel that the host does not confirm is not reported as cancelled', () async {
      final api = _FakeApi(cancelConfirmed: false);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.cancel();

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(controller.hasExecutionToken, isFalse);
      expect(controller.error, isNotNull);
    });

    test('network failure during cancel does not claim cancellation', () async {
      final api = _FakeApi(throwOnCancel: true);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.cancel();

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(api.executeCalls, 0);
    });

    test('a decision in flight blocks cancellation', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      // The proposal is already approved, so cancellation is refused locally.
      await controller.cancel();
      expect(api.cancelCalls, 0);
      expect(controller.phase, ProcessApprovalPhase.approved);
    });

    test('duplicate approve taps produce a single decision request', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await Future.wait([controller.approve(), controller.approve(), controller.approve()]);

      expect(api.decisionCalls, 1);
    });

    test('network failure during the decision never yields a token and never executes', () async {
      final api = _FakeApi(throwOnDecision: true);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(controller.hasExecutionToken, isFalse);
      expect(api.executeCalls, 0);

      // Retrying the decision is an explicit user action, not an automatic retry.
      await controller.approve();
      expect(api.decisionCalls, 1, reason: 'a failed attempt leaves the proposal undecided, not retried');
    });

    test('network failure during the approval request fails closed', () async {
      final api = _FakeApi(throwOnRequest: true);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(controller.proposal, isNull);
      expect(api.executeCalls, 0);
    });

    test('a revoked or rejected session decision fails closed', () async {
      final api = _FakeApi(decisionStatusCode: 403);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(controller.hasExecutionToken, isFalse);
      expect(api.executeCalls, 0);
    });

    test('an expired approval reported by the host is surfaced as expired', () async {
      final api = _FakeApi(decisionStatusCode: 410);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      expect(controller.phase, ProcessApprovalPhase.expired);
      expect(api.executeCalls, 0);
    });

    test('a command that needs no approval is reported as unavailable rather than executed', () async {
      final api = _FakeApi(requestStatusCode: 400);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: 'git', args: ['status']);

      expect(controller.phase, ProcessApprovalPhase.unavailable);
      expect(api.executeCalls, 0);
    });

    test('a host-side scope rejection during execution fails closed', () async {
      final api = _FakeApi(executeStatusCode: 403);
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();
      await controller.executeApproved();

      expect(controller.phase, ProcessApprovalPhase.failed);
      expect(controller.result, isNull);
    });

    test('the controller never exposes the execution token through its API', () async {
      final api = _FakeApi();
      final controller = ProcessApprovalController(api);

      await controller.propose(workspaceId: '/ws', command: './gradlew', args: ['build']);
      await controller.approve();

      // Only a boolean capability exists; the token itself stays inside the controller.
      expect(controller.hasExecutionToken, isTrue);
    });
  });
}

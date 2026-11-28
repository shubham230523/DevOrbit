/// Client-side state machine for the Gradle execution approval protocol.
///
/// The Host Bridge treats a code-executing command (the Gradle family) as a separate trust boundary:
/// requesting an approval grants nothing, and only an explicit decision on the proposal mints a
/// single-use execution token. This controller therefore never assumes success: every state
/// transition below is derived from a server response, and no execution is attempted until the
/// server has returned a token.
///
/// Security-relevant rules encoded here:
///  * `approve()` is a no-op unless a pending proposal exists (prevents an accidental one-step flow).
///  * Duplicate approve taps are ignored while a decision is in flight.
///  * A network failure leaves the proposal pending forever from this client's perspective: it never
///    auto-retries an approval and never executes without a token.
///  * A new controller instance starts in [ProcessApprovalPhase.idle]; nothing is auto-approved after
///    an app restart.
library;

/// Lifecycle of one approval attempt as observed by the client.
enum ProcessApprovalPhase {
  /// No proposal in flight.
  idle,

  /// Asking the host for a pending proposal.
  requesting,

  /// Proposal received; the user has not decided yet. Displays the exact command.
  awaitingDecision,

  /// The user rejected the proposal. Terminal.
  rejected,

  /// The proposal lapsed before a decision. Terminal.
  expired,

  /// The host rejected the request (for example a command that needs no approval). Terminal.
  unavailable,

  /// A decision was submitted and the host issues a token; execution has not started yet.
  approved,

  /// Execution submitted with the token.
  executing,

  /// The host started the process and returned its details.
  completed,

  /// Any failure that is not recoverable by retrying the same decision.
  failed,

  /// Cancellation has been sent to the host and not yet confirmed.
  cancellationRequested,

  /// The host confirmed that the proposal is no longer actionable.
  cancelled,
}

/// A pending approval proposal exactly as returned by the host.
class ProcessApprovalProposal {
  const ProcessApprovalProposal({
    required this.approvalId,
    required this.workspaceId,
    required this.command,
    required this.args,
    required this.reason,
    required this.commandDigest,
    required this.expiresAt,
  });

  final String approvalId;
  final String workspaceId;
  final String command;
  final List<String> args;
  final String reason;

  /// Opaque value that binds a decision to the exact command the host issued.
  final String commandDigest;
  final DateTime expiresAt;

  /// Human-readable, inspectable rendering of the exact command to be executed.
  String get displayCommand => '$command ${args.join(' ')}'.trim();

  bool get isExpired => DateTime.now().isAfter(expiresAt);

  factory ProcessApprovalProposal.fromJson(Map<String, dynamic> json) {
    final rawArgs = json['args'];
    return ProcessApprovalProposal(
      approvalId: json['approval_id'] as String? ?? '',
      workspaceId: json['workspace_id'] as String? ?? '',
      command: json['command'] as String? ?? '',
      args: rawArgs is List ? rawArgs.map((e) => e.toString()).toList() : const <String>[],
      reason: json['reason'] as String? ?? '',
      commandDigest: json['command_digest'] as String? ?? '',
      expiresAt: DateTime.tryParse(json['expires_at'] as String? ?? '') ?? DateTime.now(),
    );
  }
}

/// Result of requesting an approval.
class ProcessApprovalRequestResult {
  const ProcessApprovalRequestResult({this.proposal, this.statusCode = 0, this.error});

  final ProcessApprovalProposal? proposal;
  final int statusCode;
  final String? error;

  bool get isSuccess => proposal != null;
}

/// Result of submitting a decision.
class ProcessApprovalDecisionResult {
  const ProcessApprovalDecisionResult({
    this.executionToken,
    this.state,
    this.statusCode = 0,
    this.error,
  });

  final String? executionToken;
  final String? state;
  final int statusCode;
  final String? error;

  bool get isApproved => executionToken != null && executionToken!.isNotEmpty;
}

/// Result of fetching an existing proposal (agent task flow).
class ProcessApprovalFetchResult {
  const ProcessApprovalFetchResult({this.proposal, this.statusCode = 0, this.error});

  final ProcessApprovalProposal? proposal;
  final int statusCode;
  final String? error;

  bool get isSuccess => proposal != null;
}

/// Result of submitting an execution.
class ProcessExecutionResult {
  const ProcessExecutionResult({this.process, this.statusCode = 0, this.error});

  final Map<String, dynamic>? process;
  final int statusCode;
  final String? error;

  bool get isSuccess => process != null;
}

/// Transport abstraction so the controller can be tested without a network stack.
abstract class ProcessApprovalApi {
  Future<ProcessApprovalRequestResult> requestProcessApproval({
    required String workspaceId,
    required String command,
    required List<String> args,
  });

  Future<ProcessApprovalDecisionResult> decideProcessApproval({
    required String approvalId,
    required String decision,
    required String commandDigest,
  });

  Future<ProcessExecutionResult> executeProcess({
    required String workspaceId,
    required String command,
    required List<String> args,
    String? approvalToken,
  });

  /// Asks the host to abandon a pending proposal. Returns true only when the host confirms it.
  Future<bool> cancelProcessApproval({required String approvalId});

  /// Fetches an existing proposal by id (used when the proposal was created server-side by an agent
  /// task rather than by this client).
  Future<ProcessApprovalFetchResult> fetchProcessApproval({required String approvalId});
}

/// Drives the two-step approval protocol for one command.
class ProcessApprovalController {
  ProcessApprovalController(this._api);

  final ProcessApprovalApi _api;

  ProcessApprovalPhase _phase = ProcessApprovalPhase.idle;
  ProcessApprovalProposal? _proposal;
  String? _executionToken;
  String? _error;
  Map<String, dynamic>? _result;
  bool _busy = false;

  /// Optional resume hook for the agent task flow. When present, a successful approval resumes the
  /// task instead of executing the command directly from this client: the host consumes the approval
  /// authorization when the task runs.
  Future<bool> Function(String taskId, String approvalToken)? _resumeTask;
  String? _taskId;

  /// Binds this controller to an agent task that must be resumed after approval.
  void attachAgentTask(
    String taskId,
    Future<bool> Function(String taskId, String approvalToken) resumeTask,
  ) {
    _taskId = taskId;
    _resumeTask = resumeTask;
  }

  ProcessApprovalPhase get phase => _phase;
  ProcessApprovalProposal? get proposal => _proposal;
  String? get error => _error;
  Map<String, dynamic>? get result => _result;

  /// True while a network call is in flight; the UI uses it to disable every action, which prevents
  /// duplicate approval taps from producing a second decision request.
  bool get isBusy => _busy;

  /// The token is intentionally not exposed: the UI never displays or logs it.
  bool get hasExecutionToken => _executionToken != null;

  Future<void> propose({
    required String workspaceId,
    required String command,
    required List<String> args,
  }) async {
    if (_busy) return;
    _busy = true;
    _phase = ProcessApprovalPhase.requesting;
    _proposal = null;
    _executionToken = null;
    _error = null;
    _result = null;

    try {
      final response = await _api.requestProcessApproval(
        workspaceId: workspaceId,
        command: command,
        args: args,
      );
      if (response.isSuccess) {
        _proposal = response.proposal;
        _phase = response.proposal!.isExpired
            ? ProcessApprovalPhase.expired
            : ProcessApprovalPhase.awaitingDecision;
      } else if (response.statusCode == 400) {
        // The command does not require approval; the caller should execute it directly.
        _phase = ProcessApprovalPhase.unavailable;
        _error = response.error ?? 'This command does not require approval.';
      } else {
        _phase = ProcessApprovalPhase.failed;
        _error = response.error ?? 'Unable to request approval.';
      }
    } catch (e) {
      // No automatic retry: an uncertain request must never be treated as an approval.
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while requesting approval: $e';
    } finally {
      _busy = false;
    }
  }

  /// Loads a proposal created by the host (agent task flow) so the user can inspect and decide it.
  Future<void> loadProposal(String approvalId) async {
    if (_busy) return;
    _busy = true;
    _phase = ProcessApprovalPhase.requesting;
    _proposal = null;
    _executionToken = null;
    _error = null;

    try {
      final result = await _api.fetchProcessApproval(approvalId: approvalId);
      if (result.isSuccess) {
        _proposal = result.proposal;
        _phase = result.proposal!.isExpired
            ? ProcessApprovalPhase.expired
            : ProcessApprovalPhase.awaitingDecision;
      } else if (result.statusCode == 410) {
        _phase = ProcessApprovalPhase.expired;
      } else {
        _phase = ProcessApprovalPhase.failed;
        _error = result.error ?? 'Unable to load the approval proposal.';
      }
    } catch (e) {
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while loading the proposal: $e';
    } finally {
      _busy = false;
    }
  }

  /// Records the human approval decision. Requires a pending proposal with a server-issued digest.
  Future<void> approve() async {
    final proposal = _proposal;
    if (proposal == null || _busy || _phase != ProcessApprovalPhase.awaitingDecision) return;
    if (proposal.isExpired) {
      _phase = ProcessApprovalPhase.expired;
      return;
    }

    _busy = true;
    _error = null;
    try {
      final decision = await _api.decideProcessApproval(
        approvalId: proposal.approvalId,
        decision: 'approve',
        commandDigest: proposal.commandDigest,
      );
      if (decision.isApproved) {
        _executionToken = decision.executionToken;
        _phase = ProcessApprovalPhase.approved;

        // Agent task flow: the host consumes the authorization when the task resumes, so the client
        // resumes the task instead of executing the command directly.
        final resume = _resumeTask;
        final taskId = _taskId;
        if (resume != null && taskId != null) {
          _phase = ProcessApprovalPhase.executing;
          final resumed = await resume(taskId, proposal.approvalId);
          _executionToken = null;
          _phase = resumed ? ProcessApprovalPhase.completed : ProcessApprovalPhase.failed;
          if (!resumed) {
            _error = 'The host did not resume the task after approval.';
          }
        }
      } else if (decision.statusCode == 410) {
        _phase = ProcessApprovalPhase.expired;
      } else {
        _phase = ProcessApprovalPhase.failed;
        _error = decision.error ?? 'The host did not authorize this execution.';
      }
    } catch (e) {
      // The decision may or may not have been recorded; the client must not assume consent.
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while submitting the decision: $e';
    } finally {
      _busy = false;
    }
  }

  /// Records the human rejection. No token is ever produced for a rejected proposal.
  Future<void> reject() async {
    final proposal = _proposal;
    if (proposal == null || _busy || _phase != ProcessApprovalPhase.awaitingDecision) return;

    _busy = true;
    _error = null;
    try {
      await _api.decideProcessApproval(
        approvalId: proposal.approvalId,
        decision: 'reject',
        commandDigest: '',
      );
      _phase = ProcessApprovalPhase.rejected;
    } catch (e) {
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while submitting the rejection: $e';
    } finally {
      _busy = false;
    }
  }

  /// Cancels the pending proposal on the server.
  ///
  /// The previous implementation only changed local state, which left the host proposal actionable
  /// until it expired. Cancellation is now confirmed by the host; if the request fails or is not
  /// confirmed, the controller reports [ProcessApprovalPhase.failed] rather than claiming a
  /// cancellation that did not happen.
  Future<void> cancel() async {
    final proposal = _proposal;
    if (proposal == null || _busy || _phase != ProcessApprovalPhase.awaitingDecision) return;

    _busy = true;
    _phase = ProcessApprovalPhase.cancellationRequested;
    _error = null;
    try {
      final confirmed = await _api.cancelProcessApproval(approvalId: proposal.approvalId);
      if (confirmed) {
        _phase = ProcessApprovalPhase.cancelled;
        _executionToken = null;
      } else {
        // The host may still hold the proposal; never report success we cannot prove.
        _phase = ProcessApprovalPhase.failed;
        _error = 'Cancellation was not confirmed by the host; the proposal may still expire normally.';
      }
    } catch (e) {
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while cancelling: $e';
    } finally {
      _busy = false;
    }
  }

  /// Executes the approved command exactly once. Refuses without a server-issued token.
  Future<void> executeApproved() async {
    final proposal = _proposal;
    final token = _executionToken;
    if (proposal == null || token == null || _busy) return;
    if (_phase != ProcessApprovalPhase.approved) return;

    _busy = true;
    _phase = ProcessApprovalPhase.executing;
    _error = null;
    try {
      final execution = await _api.executeProcess(
        workspaceId: proposal.workspaceId,
        command: proposal.command,
        args: proposal.args,
        approvalToken: token,
      );
      if (execution.isSuccess) {
        _result = execution.process;
        _phase = ProcessApprovalPhase.completed;
        _executionToken = null;
      } else {
        _phase = ProcessApprovalPhase.failed;
        _error = execution.error ?? 'Execution was rejected by the host.';
      }
    } catch (e) {
      _phase = ProcessApprovalPhase.failed;
      _error = 'Network error while starting execution: $e';
    } finally {
      _busy = false;
    }
  }
}

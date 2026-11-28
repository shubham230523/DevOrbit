import 'package:flutter/material.dart';

import '../../../tasks/application/process_approval_controller.dart';

/// The user's explicit choice for a process approval proposal.
enum ProcessApprovalChoice { approve, reject, cancel }

/// Shows the exact command a host-issued process approval will run.
///
/// The dialog is deliberately explicit: the executable, every argument, the workspace, the host's
/// policy reason and the expiry are all visible before an Approve action is possible. Long argument
/// vectors are expandable rather than truncated, because a shortened preview would let a user approve
/// something different from what executes. Execution tokens are never passed to this widget.
class ProcessApprovalDialog extends StatefulWidget {
  const ProcessApprovalDialog({super.key, required this.proposal});

  final ProcessApprovalProposal proposal;

  @override
  State<ProcessApprovalDialog> createState() => _ProcessApprovalDialogState();
}

class _ProcessApprovalDialogState extends State<ProcessApprovalDialog> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final proposal = widget.proposal;
    final args = proposal.args;
    final hasManyArgs = args.length > 3;
    final visibleArgs = _expanded || !hasManyArgs ? args : args.take(3).toList();

    return AlertDialog(
      title: const Text('Approve command execution?'),
      content: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text(
              'The assistant wants to run a build command that executes project-defined code. '
              'Review the exact command before approving.',
            ),
            const SizedBox(height: 12),
            const Text('Executable', style: TextStyle(fontWeight: FontWeight.bold)),
            SelectableText(
              proposal.command,
              style: const TextStyle(fontFamily: 'monospace'),
            ),
            const SizedBox(height: 8),
            const Text('Arguments', style: TextStyle(fontWeight: FontWeight.bold)),
            if (args.isEmpty)
              const Text('(none)', style: TextStyle(fontStyle: FontStyle.italic))
            else
              ...visibleArgs.map(
                (arg) => SelectableText(
                  '• $arg',
                  style: const TextStyle(fontFamily: 'monospace'),
                ),
              ),
            if (hasManyArgs)
              TextButton(
                onPressed: () => setState(() => _expanded = !_expanded),
                child: Text(_expanded ? 'Show fewer arguments' : 'Show all ${args.length} arguments'),
              ),
            const SizedBox(height: 8),
            const Text('Workspace', style: TextStyle(fontWeight: FontWeight.bold)),
            SelectableText(proposal.workspaceId),
            const SizedBox(height: 8),
            const Text('Reason', style: TextStyle(fontWeight: FontWeight.bold)),
            Text(proposal.reason),
            const SizedBox(height: 8),
            const Text('Expires', style: TextStyle(fontWeight: FontWeight.bold)),
            Text(proposal.expiresAt.toLocal().toString()),
            if (proposal.isExpired) ...[
              const SizedBox(height: 12),
              const Text(
                'This proposal has expired and can no longer be approved.',
                style: TextStyle(color: Colors.redAccent),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(ProcessApprovalChoice.cancel),
          child: const Text('Cancel'),
        ),
        TextButton(
          onPressed: () => Navigator.of(context).pop(ProcessApprovalChoice.reject),
          child: const Text('Reject'),
        ),
        ElevatedButton(
          onPressed: proposal.isExpired
              ? null
              : () => Navigator.of(context).pop(ProcessApprovalChoice.approve),
          child: const Text('Approve'),
        ),
      ],
    );
  }
}

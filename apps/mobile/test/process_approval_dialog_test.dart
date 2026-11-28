import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:devorbit_mobile/features/tasks/application/process_approval_controller.dart';
import 'package:devorbit_mobile/features/travel_coding/presentation/widgets/process_approval_dialog.dart';

ProcessApprovalProposal _proposal({List<String>? args, Duration? expiresIn}) {
  return ProcessApprovalProposal(
    approvalId: 'pappr-secret-id',
    workspaceId: '/home/dev/project',
    command: './gradlew',
    args: args ?? const ['assembleRelease', '--stacktrace'],
    reason: 'gradle executes project-defined build logic',
    commandDigest: 'digest-abc',
    expiresAt: DateTime.now().add(expiresIn ?? const Duration(minutes: 10)),
  );
}

Future<ProcessApprovalChoice?> _show(WidgetTester tester, ProcessApprovalProposal proposal) async {
  ProcessApprovalChoice? choice;
  await tester.pumpWidget(MaterialApp(
    home: Builder(
      builder: (context) => ElevatedButton(
        onPressed: () async {
          choice = await showDialog<ProcessApprovalChoice>(
            context: context,
            builder: (_) => ProcessApprovalDialog(proposal: proposal),
          );
        },
        child: const Text('open'),
      ),
    ),
  ));
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  return choice;
}

void main() {
  testWidgets('shows the exact executable, arguments, workspace, reason and expiry', (tester) async {
    await _show(tester, _proposal());

    expect(find.text('./gradlew'), findsOneWidget);
    expect(find.text('• assembleRelease'), findsOneWidget);
    expect(find.text('• --stacktrace'), findsOneWidget);
    expect(find.text('/home/dev/project'), findsOneWidget);
    expect(find.text('gradle executes project-defined build logic'), findsOneWidget);
    expect(find.text('Approve'), findsOneWidget);
    expect(find.text('Reject'), findsOneWidget);
    expect(find.text('Cancel'), findsOneWidget);
  });

  testWidgets('never renders the approval id or an execution token', (tester) async {
    await _show(tester, _proposal());

    expect(find.text('pappr-secret-id'), findsNothing);
    expect(find.textContaining('pexec-'), findsNothing);
    expect(find.textContaining('digest-abc'), findsNothing);
  });

  testWidgets('long argument vectors are expandable rather than truncated', (tester) async {
    await _show(tester, _proposal(args: const ['a', 'b', 'c', 'd', 'e']));

    expect(find.text('• d'), findsNothing);
    expect(find.text('Show all 5 arguments'), findsOneWidget);

    await tester.tap(find.text('Show all 5 arguments'));
    await tester.pumpAndSettle();

    expect(find.text('• d'), findsOneWidget);
    expect(find.text('• e'), findsOneWidget);
  });

  testWidgets('approve returns the approve choice', (tester) async {
    final choice = await _openAndTap(tester, _proposal(), 'Approve');
    expect(choice, ProcessApprovalChoice.approve);
  });

  testWidgets('reject returns the reject choice', (tester) async {
    final choice = await _openAndTap(tester, _proposal(), 'Reject');
    expect(choice, ProcessApprovalChoice.reject);
  });

  testWidgets('cancel returns the cancel choice', (tester) async {
    final choice = await _openAndTap(tester, _proposal(), 'Cancel');
    expect(choice, ProcessApprovalChoice.cancel);
  });

  testWidgets('an expired proposal cannot be approved', (tester) async {
    await _show(tester, _proposal(expiresIn: const Duration(seconds: -1)));

    expect(find.textContaining('has expired'), findsOneWidget);
    final approve = tester.widget<ElevatedButton>(
      find.widgetWithText(ElevatedButton, 'Approve'),
    );
    expect(approve.onPressed, isNull);
  });
}

Future<ProcessApprovalChoice?> _openAndTap(
  WidgetTester tester,
  ProcessApprovalProposal proposal,
  String label,
) async {
  ProcessApprovalChoice? choice;
  await tester.pumpWidget(MaterialApp(
    home: Builder(
      builder: (context) => ElevatedButton(
        onPressed: () async {
          choice = await showDialog<ProcessApprovalChoice>(
            context: context,
            builder: (_) => ProcessApprovalDialog(proposal: proposal),
          );
        },
        child: const Text('open'),
      ),
    ),
  ));
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  await tester.tap(find.text(label));
  await tester.pumpAndSettle();
  return choice;
}

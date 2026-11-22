import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/features/travel_coding/presentation/travel_coding_screen.dart';
import 'package:devorbit_mobile/features/travel_coding/presentation/widgets/approval_dialog.dart';

void main() {
  testWidgets('TravelCodingScreen loads title and prompt input field', (WidgetTester tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: TravelCodingScreen(),
      ),
    );

    expect(find.text('Travel AI Coding Cockpit'), findsOneWidget);
    expect(find.text('Enter AI coding instructions for Gemini...'), findsOneWidget);
  });

  testWidgets('TravelApprovalDialog renders file diff proposal and buttons', (WidgetTester tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: TravelApprovalDialog(
          taskId: 'task-test-100',
          toolName: 'edit_file',
          targetPath: 'lib/main.dart',
          expectedSHA256: 'a1b2c3d4e5f67890',
          targetContent: 'oldContent()',
          replacement: 'newContent()',
          reason: 'Edit file lib/main.dart',
        ),
      ),
    );

    expect(find.text('File Change Approval'), findsOneWidget);
    expect(find.text('lib/main.dart'), findsOneWidget);
    expect(find.text('- oldContent()'), findsOneWidget);
    expect(find.text('+ newContent()'), findsOneWidget);
    expect(find.text('Approve & Execute'), findsOneWidget);
    expect(find.text('Reject'), findsOneWidget);
  });
}

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/core/security/secure_storage.dart';
import 'package:devorbit_mobile/features/dashboard/presentation/dashboard_screen.dart';

void main() {
  testWidgets('DashboardScreen displays Pairing Required when no credentials available', (WidgetTester tester) async {
    final storage = SecureStorageService();
    await storage.clearPairedSession();

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: DashboardScreen(
            secureStorage: storage,
          ),
        ),
      ),
    );

    await tester.pump();

    // Verify pairing required status chip and warning card
    expect(find.text('PAIRING REQUIRED'), findsOneWidget);
    expect(find.text('Host Pairing Required'), findsOneWidget);
    expect(find.text('Pair Host Machine (Scan QR)'), findsOneWidget);

    // Verify Connect button is disabled when pairing information is missing
    final connectButtonFinder = find.widgetWithText(ElevatedButton, 'Connect');
    expect(connectButtonFinder, findsOneWidget);
    final connectButton = tester.widget<ElevatedButton>(connectButtonFinder);
    expect(connectButton.onPressed, isNull);
  });

  testWidgets('DashboardScreen accepts injected paired credentials and enables connect', (WidgetTester tester) async {
    final storage = SecureStorageService();
    await storage.savePairedSession(
      machineId: 'mach-flutter-test-01',
      deviceId: 'dev-flutter-test-01',
      sessionId: 'sess-flutter-test-01',
      token: 'token-flutter-test-secret',
      masterSecret: 'secret-flutter-test-master-32b!',
      brokerUrl: 'ws://localhost:8081',
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: DashboardScreen(
            secureStorage: storage,
          ),
        ),
      ),
    );

    await tester.pump();

    // With valid credentials loaded, pairing is not required
    expect(find.text('PAIRING REQUIRED'), findsNothing);
    expect(find.text('Host Pairing Required'), findsNothing);

    // Connect button should now be enabled
    final connectButtonFinder = find.widgetWithText(ElevatedButton, 'Connect');
    final connectButton = tester.widget<ElevatedButton>(connectButtonFinder);
    expect(connectButton.onPressed, isNotNull);
  });
}

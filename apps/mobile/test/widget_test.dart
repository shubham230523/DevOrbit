import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/main.dart';

void main() {
  testWidgets('DevOrbit app navigation loads dashboard', (WidgetTester tester) async {
    await tester.pumpWidget(const DevOrbitApp());
    await tester.pump();

    expect(find.text('DevOrbit Dashboard'), findsOneWidget);

    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pump();

    expect(find.text('Live Discovered Projects'), findsOneWidget);
  });
}



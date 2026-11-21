import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/main.dart';

void main() {
  testWidgets('DevOrbit app navigation loads dashboard', (WidgetTester tester) async {
    await tester.pumpWidget(const DevOrbitApp());

    expect(find.text('DevOrbit Dashboard'), findsOneWidget);
    expect(find.text('Live Discovered Projects'), findsOneWidget);
  });
}

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'core/theme/app_theme.dart';
import 'features/dashboard/presentation/dashboard_screen.dart';
import 'features/travel_coding/presentation/travel_coding_screen.dart';
import 'features/projects/presentation/project_explorer_screen.dart';
import 'features/editor/presentation/code_editor_screen.dart';
import 'features/machines/presentation/machine_management_screen.dart';
import 'features/settings/presentation/settings_screen.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setSystemUIOverlayStyle(
    const SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: Brightness.light,
      systemNavigationBarColor: Color(0xFF121212),
      systemNavigationBarDividerColor: Colors.transparent,
      systemNavigationBarIconBrightness: Brightness.light,
    ),
  );
  runApp(const DevOrbitApp());
}

class DevOrbitApp extends StatelessWidget {
  const DevOrbitApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'DevOrbit Workspace',
      theme: AppTheme.darkTheme,
      home: const MainNavigationScreen(),
      debugShowCheckedModeBanner: false,
    );
  }
}

class MainNavigationScreen extends StatefulWidget {
  const MainNavigationScreen({super.key});

  @override
  State<MainNavigationScreen> createState() => _MainNavigationScreenState();
}

class _MainNavigationScreenState extends State<MainNavigationScreen> {
  int _selectedIndex = 0;
  String? _selectedFilePath;

  static const List<String> _titles = [
    'DevOrbit Dashboard',
    'Travel AI Cockpit',
    'Project Explorer',
    'Host Machines',
    'Settings',
    'Code Editor',
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_selectedIndex < _titles.length ? _titles[_selectedIndex] : 'DevOrbit Workspace'),
      ),
      body: IndexedStack(
        index: _selectedIndex,
        children: [
          const DashboardScreen(),
          const TravelCodingScreen(),
          ProjectExplorerScreen(
            onFileSelected: (path) {
              setState(() {
                _selectedFilePath = path;
                _selectedIndex = 5;
              });
            },
          ),
          const MachineManagementScreen(),
          const SettingsScreen(),
          CodeEditorScreen(filePath: _selectedFilePath),
        ],
      ),
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          color: Color(0xFF121212),
          border: Border(
            top: BorderSide(color: Color(0xFF222222), width: 0.5),
          ),
        ),
        child: BottomNavigationBar(
          currentIndex: _selectedIndex > 4 ? 2 : _selectedIndex,
          onTap: (index) {
            setState(() {
              _selectedIndex = index;
            });
          },
          type: BottomNavigationBarType.fixed,
          backgroundColor: const Color(0xFF121212),
          selectedItemColor: const Color(0xFFBB86FC),
          unselectedItemColor: Colors.grey,
          elevation: 0,
          items: const [
            BottomNavigationBarItem(
              icon: Icon(Icons.dashboard),
              label: 'Home',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.auto_awesome),
              label: 'Travel AI',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.folder),
              label: 'Projects',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.computer),
              label: 'Machines',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.settings),
              label: 'Settings',
            ),
          ],
        ),
      ),
    );
  }
}


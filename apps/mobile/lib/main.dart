import 'package:flutter/material.dart';
import 'core/theme/app_theme.dart';
import 'features/dashboard/presentation/dashboard_screen.dart';
import 'features/projects/presentation/project_explorer_screen.dart';
import 'features/editor/presentation/code_editor_screen.dart';
import 'features/ai_workspace/presentation/ai_workspace_screen.dart';
import 'features/machines/presentation/machine_management_screen.dart';
import 'features/settings/presentation/settings_screen.dart';

void main() {
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

  static const List<Widget> _screens = [
    DashboardScreen(),
    ProjectExplorerScreen(),
    CodeEditorScreen(),
    AiWorkspaceScreen(),
    MachineManagementScreen(),
    SettingsScreen(),
  ];

  static const List<String> _titles = [
    'DevOrbit Dashboard',
    'Project Explorer',
    'Code Editor',
    'AI Workspace',
    'Host Machines',
    'Settings',
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_titles[_selectedIndex]),
      ),
      body: _screens[_selectedIndex],
      bottomNavigationBar: BottomNavigationBar(
        currentIndex: _selectedIndex,
        onTap: (index) {
          setState(() {
            _selectedIndex = index;
          });
        },
        items: const [
          BottomNavigationBarItem(
            icon: Icon(Icons.dashboard),
            label: 'Home',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.folder),
            label: 'Projects',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.code),
            label: 'Editor',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.auto_awesome),
            label: 'AI Workspace',
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
    );
  }
}

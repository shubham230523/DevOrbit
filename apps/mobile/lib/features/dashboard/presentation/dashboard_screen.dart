import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';

class DashboardScreen extends StatefulWidget {
  final String hostBaseUrl;

  const DashboardScreen({
    super.key,
    this.hostBaseUrl = 'http://10.0.2.2:8082', // Android emulator loopback host bridge endpoint
  });

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  late HostBridgeApiClient _apiClient;
  bool _isLoading = true;
  bool _isOnline = false;
  Map<String, dynamic>? _healthData;
  List<dynamic> _workspaces = [];
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    _apiClient = HostBridgeApiClient(baseUrl: widget.hostBaseUrl);
    _fetchLiveHostData();
  }

  Future<void> _fetchLiveHostData() async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final health = await _apiClient.getHealth();
    if (health != null && health['status'] == 'ok') {
      final workspaces = await _apiClient.listWorkspaces();
      if (mounted) {
        setState(() {
          _isOnline = true;
          _healthData = health;
          _workspaces = workspaces;
          _isLoading = false;
        });
      }
    } else {
      if (mounted) {
        setState(() {
          _isOnline = false;
          _healthData = null;
          _workspaces = [];
          _isLoading = false;
          _errorMessage = 'Unable to reach Host Bridge at ${widget.hostBaseUrl}';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final hostname = _healthData?['hostname'] ?? 'Desktop Host';
    final osType = _healthData?['os'] ?? 'Unknown OS';
    final arch = _healthData?['arch'] ?? '';
    final latency = _healthData?['latency_ms'] ?? 0;
    final version = _healthData?['host_bridge_version'] ?? 'v1.2.1';
    final allowedRoots = _healthData?['allowed_workspaces_count'] ?? 0;

    return RefreshIndicator(
      onRefresh: _fetchLiveHostData,
      child: ListView(
        padding: const EdgeInsets.all(16.0),
        children: [
          // Real Machine Health Status Card
          Card(
            child: ListTile(
              leading: Icon(
                Icons.computer,
                color: _isOnline ? Colors.green : Colors.red,
                size: 32,
              ),
              title: Text(
                _isOnline ? '$hostname ($osType/$arch)' : 'Host Offline',
                style: const TextStyle(fontWeight: FontWeight.bold),
              ),
              subtitle: _isOnline
                  ? Text('Host Bridge $version • Latency: ${latency}ms\nAllowed Roots: $allowedRoots')
                  : Text(_errorMessage ?? 'Host Bridge daemon disconnected'),
              isThreeLine: _isOnline,
              trailing: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Chip(
                    label: Text(_isOnline ? 'Active' : 'Offline'),
                    backgroundColor: _isOnline
                        ? Colors.green.withValues(alpha: 0.2)
                        : Colors.red.withValues(alpha: 0.2),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // Real Discovered Projects List Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        'Live Discovered Projects',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      IconButton(
                        icon: const Icon(Icons.refresh, size: 20),
                        onPressed: _fetchLiveHostData,
                        tooltip: 'Refresh Host Status',
                      ),
                    ],
                  ),
                  const Divider(),
                  if (_isLoading)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else if (!_isOnline)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text(
                        'Host is offline. Start the Host Bridge daemon on your PC to view live projects.',
                        style: TextStyle(color: Colors.grey),
                      ),
                    )
                  else if (_workspaces.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text('No Android Studio or Gradle projects found under host allowed roots.'),
                    )
                  else
                    ..._workspaces.map((ws) {
                      final name = ws['name'] ?? 'Project';
                      final rootPath = ws['root_path'] ?? '';
                      final isAndroid = ws['is_android_project'] == true;

                      return ListTile(
                        leading: Icon(
                          isAndroid ? Icons.android : Icons.folder,
                          color: isAndroid ? Colors.teal : Colors.amber,
                        ),
                        title: Text(name.toString()),
                        subtitle: Text('Path: $rootPath'),
                        trailing: const Icon(Icons.chevron_right),
                      );
                    }),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // AI Agent Active Status Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'AI Agent Integration Status',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const Divider(),
                  ListTile(
                    leading: const Icon(Icons.auto_awesome, color: Colors.amber),
                    title: const Text('Google Antigravity Agent'),
                    subtitle: Text('Model: Gemini 3.8 Flash • Router: ${widget.hostBaseUrl}'),
                    trailing: Chip(
                      label: Text(_isOnline ? 'Ready' : 'Unavailable'),
                      backgroundColor: _isOnline
                          ? Colors.amber.withValues(alpha: 0.2)
                          : Colors.grey.withValues(alpha: 0.2),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

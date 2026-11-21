import 'package:flutter/material.dart';
import '../../../core/network/api_client.dart';

class DashboardScreen extends StatefulWidget {
  final String? initialHostBaseUrl;

  const DashboardScreen({
    super.key,
    this.initialHostBaseUrl,
  });

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  late TextEditingController _hostController;
  late HostBridgeApiClient _apiClient;
  bool _isLoading = true;
  bool _isOnline = false;
  Map<String, dynamic>? _healthData;
  List<dynamic> _workspaces = [];
  bool _workspaceFetchError = false;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    final defaultUrl = widget.initialHostBaseUrl ?? 'http://10.0.2.2:8082';
    _hostController = TextEditingController(text: defaultUrl);
    _apiClient = HostBridgeApiClient(baseUrl: defaultUrl);
    _fetchLiveHostData();
  }

  Future<void> _fetchLiveHostData() async {
    final url = _hostController.text.trim();
    if (url.isEmpty) return;

    _apiClient = HostBridgeApiClient(baseUrl: url);

    setState(() {
      _isLoading = true;
      _errorMessage = null;
      _workspaceFetchError = false;
    });

    final health = await _apiClient.getHealth();
    if (health != null && health['status'] == 'ok') {
      final wsRes = await _apiClient.listWorkspaces();
      if (mounted) {
        setState(() {
          _isOnline = true;
          _healthData = health;
          if (wsRes['success'] == true) {
            _workspaces = wsRes['workspaces'] as List<dynamic>;
            _workspaceFetchError = false;
          } else {
            _workspaces = [];
            _workspaceFetchError = true;
          }
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
          _errorMessage = 'Unable to establish session with Host Bridge at $url';
        });
      }
    }
  }

  @override
  void dispose() {
    _hostController.dispose();
    super.dispose();
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
          // Endpoint Selection Input Card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(12.0),
              child: Row(
                children: [
                  const Icon(Icons.link, color: Colors.purpleAccent),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _hostController,
                      decoration: const InputDecoration(
                        labelText: 'Host Bridge Endpoint',
                        hintText: 'e.g. http://192.168.1.100:8082',
                        isDense: true,
                        border: OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: _isLoading ? null : _fetchLiveHostData,
                    child: const Text('Connect'),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),

          // Real Machine Health Status Card
          Card(
            child: ListTile(
              leading: Icon(
                Icons.computer,
                color: _isOnline ? Colors.green : Colors.red,
                size: 32,
              ),
              title: Text(
                _isOnline ? '$hostname ($osType/$arch)' : 'Host Session Offline',
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
                    label: Text(_isOnline ? 'CONNECTED' : 'DISCONNECTED'),
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
                        'Host is offline. Connect to Host Bridge daemon to view live projects.',
                        style: TextStyle(color: Colors.grey),
                      ),
                    )
                  else if (_workspaceFetchError)
                    const Padding(
                      padding: EdgeInsets.all(16.0),
                      child: Text(
                        'Error retrieving workspaces from host: Authorization or network failure.',
                        style: TextStyle(color: Colors.orangeAccent),
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
        ],
      ),
    );
  }
}

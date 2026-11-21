import 'package:devorbit_mobile/core/security/secure_storage.dart';

/// Test-isolated in-memory driver used strictly in tests to simulate storage,
/// restarts across service instances, write failures, and partial writes.
class TestInMemoryStorageDriver implements SecureStorageDriver {
  final Map<String, String> backingStore;
  bool shouldFailWrites = false;
  bool shouldFailReads = false;

  TestInMemoryStorageDriver([Map<String, String>? initialData])
      : backingStore = initialData ?? <String, String>{};

  @override
  Future<void> write({required String key, required String value}) async {
    if (shouldFailWrites) {
      throw SecureStorageException('Simulated write failure for $key');
    }
    backingStore[key] = value;
  }

  @override
  Future<String?> read({required String key}) async {
    if (shouldFailReads) {
      throw SecureStorageException('Simulated read failure for $key');
    }
    return backingStore[key];
  }

  @override
  Future<void> delete({required String key}) async {
    backingStore.remove(key);
  }

  @override
  Future<void> deleteAll() async {
    backingStore.clear();
  }

  @override
  Future<Map<String, String>> readAll() async {
    if (shouldFailReads) {
      throw SecureStorageException('Simulated readAll failure');
    }
    return Map<String, String>.from(backingStore);
  }
}

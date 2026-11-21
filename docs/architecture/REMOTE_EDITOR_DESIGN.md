# DevOrbit — Remote Code Editor Design Specification

## 1. Editor Architecture

The remote code editor (`CodeEditorScreen`) allows developers to inspect and edit code files stored on the remote workstation host.

```
Mobile Code Editor Buffer (Local Edition)
                 |
         Save Button Pressed
                 |
      HostBridgeApiClient.writeFile()
 (Sends new_content + base_sha256)
                 |
         Host Bridge StorageEngine
                 |
        +--------+--------+
        |                 |
  SHA256 Match       SHA256 Mismatch
        |                 |
 Atomic Write      HTTP 409 Conflict
        |                 |
  HTTP 200 OK      ConflictResolutionModal
              (Keep Local / Accept Remote)
```

## 2. Conflict Detection & Resolution Workflow

1. **Base Hash Tracking**: When a file is opened, the client stores its SHA256 content hash (`base_sha256`).
2. **Conflict Verification**: Upon saving, the host compares `base_sha256` against the current file hash on disk.
3. **Conflict Resolution Modal**: If the file was modified concurrently on the host, Host Bridge returns `HTTP 409 Conflict` with the current host content. The mobile app presents `ConflictResolutionModal` allowing the developer to:
   - **Keep Local Edits**: Force overwrites the host file with local buffer contents.
   - **Accept Remote Version**: Reloads the current desktop host content into the editor buffer.

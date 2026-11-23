import 'dart:convert';

class QrPairingPayload {
  final String version;
  final String challengeId;
  final String hostId;
  final String hostName;
  final String pairingPayload;
  final String pairingSecret;
  final String confirmUrl;
  final String? brokerUrl;
  final DateTime expiresAt;
  final String rawExpiresAt;

  QrPairingPayload({
    required this.version,
    required this.challengeId,
    required this.hostId,
    required this.hostName,
    required this.pairingPayload,
    required this.pairingSecret,
    required this.confirmUrl,
    this.brokerUrl,
    required this.expiresAt,
    required this.rawExpiresAt,
  });

  bool get isExpired => DateTime.now().isAfter(expiresAt);

  factory QrPairingPayload.fromJson(Map<String, dynamic> json) {
    final v = json['v']?.toString();
    if (v != '1.0' && v != '1') {
      throw FormatException('Unsupported QR pairing protocol version: $v');
    }

    final challengeId = json['challenge_id']?.toString() ?? json['id']?.toString();
    final hostId = json['host_id']?.toString() ?? json['machine_id']?.toString();
    final hostName = json['host_name']?.toString() ?? 'Desktop Host';
    final pairingPayload = json['pairing_payload']?.toString() ?? json['payload']?.toString();
    final pairingSecret = json['pairing_secret']?.toString() ?? '';
    final confirmUrl = json['confirm_url']?.toString() ?? json['control_plane_url']?.toString();
    final brokerUrl = json['broker_url']?.toString();
    final expiresAtStr = json['expires_at']?.toString();

    if (challengeId == null || challengeId.isEmpty) {
      throw const FormatException('QR pairing payload missing challenge_id');
    }
    if (hostId == null || hostId.isEmpty) {
      throw const FormatException('QR pairing payload missing host_id');
    }
    if (pairingPayload == null || pairingPayload.isEmpty) {
      throw const FormatException('QR pairing payload missing pairing_payload');
    }
    if (confirmUrl == null || confirmUrl.isEmpty) {
      throw const FormatException('QR pairing payload missing confirm_url / control_plane_url');
    }
    if (expiresAtStr == null || expiresAtStr.isEmpty) {
      throw const FormatException('QR pairing payload missing expires_at');
    }

    // Validate absolute URI format, scheme, authority, and path
    final uri = Uri.tryParse(confirmUrl);
    if (uri == null || !uri.hasScheme || !uri.hasAuthority || uri.host.isEmpty) {
      throw FormatException('confirm_url must be a valid absolute URI (http or https): $confirmUrl');
    }
    final scheme = uri.scheme.toLowerCase();
    if (scheme != 'http' && scheme != 'https') {
      throw FormatException('Unsupported URI scheme in confirm_url: $scheme. Must be http or https.');
    }
    if (uri.hasPort && (uri.port <= 0 || uri.port > 65535)) {
      throw FormatException('Invalid port in confirm_url: ${uri.port}');
    }
    if (!uri.path.endsWith('/api/v1/pairing/confirm')) {
      throw FormatException('confirm_url path must end with /api/v1/pairing/confirm, got: ${uri.path}');
    }
    if (uri.userInfo.isNotEmpty) {
      throw const FormatException('confirm_url must not contain embedded user credentials');
    }
    if (uri.hasQuery) {
      throw const FormatException('confirm_url must not contain query parameters');
    }
    if (uri.hasFragment) {
      throw const FormatException('confirm_url must not contain URL fragments');
    }

    if (brokerUrl != null && brokerUrl.isNotEmpty) {
      final bUri = Uri.tryParse(brokerUrl);
      if (bUri == null || !bUri.hasScheme || !bUri.hasAuthority || bUri.host.isEmpty) {
        throw FormatException('broker_url must be a valid absolute URI: $brokerUrl');
      }
      final bScheme = bUri.scheme.toLowerCase();
      if (bScheme != 'ws' && bScheme != 'wss') {
        throw FormatException('Unsupported URI scheme in broker_url: $bScheme. Must be ws or wss.');
      }
      if (bUri.userInfo.isNotEmpty) {
        throw const FormatException('broker_url must not contain embedded user credentials');
      }
    }

    final expiresAt = DateTime.parse(expiresAtStr);

    // Normalise expiresAtStr to RFC3339 seconds-precision (no milliseconds)
    // so it always matches what the Go host stored in ch.ExpiresAtStr.
    // Go uses time.RFC3339 which produces "2006-01-02T15:04:05Z07:00" (no sub-seconds).
    // Dart's DateTime.parse accepts both; toIso8601String() may add .000 — we strip it.
    final normalisedExpiresAtStr = _normaliseRFC3339(expiresAtStr);

    return QrPairingPayload(
      version: v!,
      challengeId: challengeId,
      hostId: hostId,
      hostName: hostName,
      pairingPayload: pairingPayload,
      pairingSecret: pairingSecret,
      confirmUrl: confirmUrl,
      brokerUrl: brokerUrl,
      expiresAt: expiresAt,
      rawExpiresAt: normalisedExpiresAtStr,
    );
  }

  /// Strips sub-second precision from an RFC3339 timestamp so it matches
  /// Go's time.RFC3339 format exactly (e.g. "2026-11-23T18:27:35Z" not
  /// "2026-11-23T18:27:35.000Z").
  static String _normaliseRFC3339(String s) {
    // Remove fractional seconds: replace ".NNN" before "Z" or "+HH:MM" / "-HH:MM"
    return s.replaceFirst(RegExp(r'\.\d+(?=[Z+\-])'), '');
  }

  factory QrPairingPayload.fromRawString(String raw) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, dynamic>) {
        throw const FormatException('QR code content is not a JSON object');
      }
      return QrPairingPayload.fromJson(decoded);
    } on FormatException {
      rethrow;
    } catch (e) {
      throw FormatException('Failed to parse QR pairing payload: $e');
    }
  }

  Map<String, dynamic> toJson() {
    return {
      'v': version,
      'challenge_id': challengeId,
      'host_id': hostId,
      'host_name': hostName,
      'pairing_payload': pairingPayload,
      if (pairingSecret.isNotEmpty) 'pairing_secret': pairingSecret,
      'confirm_url': confirmUrl,
      if (brokerUrl != null && brokerUrl!.isNotEmpty) 'broker_url': brokerUrl,
      // IMPORTANT: preserve rawExpiresAt exactly as received from the host
      // so the HMAC proof input string never diverges due to millisecond
      // formatting differences (Dart toIso8601String adds .000, Go RFC3339 does not).
      'expires_at': rawExpiresAt,
    };
  }
}

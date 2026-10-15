import Foundation
import Security
import ScratchpadNative

private enum ExitCode: Int32 {
    case success = 0
    case failed = 1
    case unlockRequired = 2
    case unavailable = 3
}

private enum KeychainKey {
    static let account = "local-capture"
    static let length = 32

    static func status(for arguments: KeychainHelperArguments) -> ExitCode {
        switch arguments.operation {
        case .get: return read(service: arguments.identifier)
        case .create: return create(service: arguments.identifier)
        }
    }

    private static func identity(service: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecUseDataProtectionKeychain as String: true,
        ]
    }

    private static func read(service: String) -> ExitCode {
        var query = identity(service: service)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let key = result as? Data else {
            return map(status)
        }
        guard key.count == length else { return .unavailable }
        FileHandle.standardOutput.write(key)
        return .success
    }

    private static func create(service: String) -> ExitCode {
        var key = Data(count: length)
        let randomStatus = key.withUnsafeMutableBytes { bytes in
            SecRandomCopyBytes(kSecRandomDefault, length, bytes.baseAddress!)
        }
        guard randomStatus == errSecSuccess else { return map(randomStatus) }

        var item = identity(service: service)
        item[kSecValueData as String] = key
        item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else {
            switch KeychainHelperArguments.createFailureDisposition(isDuplicateItem: status == errSecDuplicateItem) {
            case .loadExisting:
                // A previous DB initialization may have created the key and
                // then failed before committing the database. Never replace it.
                return read(service: service)
            case .fail:
                return map(status)
            }
        }
        FileHandle.standardOutput.write(key)
        return .success
    }

    private static func map(_ status: OSStatus) -> ExitCode {
        switch status {
        case errSecInteractionNotAllowed, errSecAuthFailed:
            return .unlockRequired
        case errSecItemNotFound:
            return .unavailable
        default:
            return .failed
        }
    }
}

guard let arguments = KeychainHelperArguments(arguments: Array(CommandLine.arguments.dropFirst())) else {
    exit(ExitCode.failed.rawValue)
}

private let result = KeychainKey.status(for: arguments)
if result != .success {
    // Exit status is the only diagnostic: Security.framework messages can
    // contain OS or Keychain details that should not reach service logs.
    fputs("queue key unavailable (code \(result.rawValue))\n", stderr)
}
exit(result.rawValue)

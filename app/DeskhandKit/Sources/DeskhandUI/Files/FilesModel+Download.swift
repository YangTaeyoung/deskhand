import DeskhandClient
import Foundation

extension FilesModel {
    /// Pulls the remote file at `remotePath` into `folder` on this Mac, under a free name when the
    /// folder has a file of that name, and follows it in the transfer queue. Creates `folder`
    /// first unless the backend keeps pulled files in memory. Returns the `transfer_id`.
    @discardableResult
    public func download(remotePath: String, toFolder folder: String) async throws -> String {
        if overlay == nil {
            try FileManager.default.createDirectory(atPath: folder, withIntermediateDirectories: true)
        }
        transfers.start()
        let name = FilePath.lastComponent(remotePath, style: remote.style)
        let request = TransferRequest(hostID: hostID, direction: .pull, local: FilePath.join(folder, name, style: .posix),
                                      remote: remotePath, onConflict: .rename)
        let started = try await backend.transferStart(request)
        await transfers.track(started.transferID)
        return started.transferID
    }
}

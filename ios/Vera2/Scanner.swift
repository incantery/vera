import AVFoundation
import SwiftUI

// The camera, looking for one thing.
//
// A UIViewControllerRepresentable rather than DataScannerViewController
// because this needs to run on anything from iOS 18, and a QR code is
// the one job AVCaptureMetadataOutput has always done well.

struct QRScanner: UIViewControllerRepresentable {
    let onFound: (String) -> Void

    func makeUIViewController(context: Context) -> ScannerController {
        let controller = ScannerController()
        controller.onFound = onFound
        return controller
    }

    func updateUIViewController(_ controller: ScannerController, context: Context) {
        controller.onFound = onFound
    }
}

/// AVCaptureSession documents that startRunning blocks — so it must
/// leave the main thread — and that its methods may be called from
/// another thread. Swift 6 cannot see that promise in a header, so it
/// gets stated here rather than worked around.
private struct Running: @unchecked Sendable {
    let session: AVCaptureSession
    func start() { session.startRunning() }
    func stop() { session.stopRunning() }
}

final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onFound: ((String) -> Void)?

    private let session = AVCaptureSession()
    private var preview: AVCaptureVideoPreviewLayer?
    private var found = false

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input)
        else { return }
        session.addInput(input)

        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        // Set AFTER adding to the session, or the type is not yet
        // available and this throws.
        output.metadataObjectTypes = [.qr]

        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspectFill
        view.layer.addSublayer(preview)
        self.preview = preview

        let running = Running(session: session)
        Task.detached { running.start() }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        let running = Running(session: session)
        Task.detached { running.stop() }
    }

    // The delegate queue is .main (set above), so this genuinely runs
    // where it claims to. Only the string crosses — AVMetadataObject is
    // not Sendable and has no business travelling.
    nonisolated func metadataOutput(
        _ output: AVCaptureMetadataOutput,
        didOutput objects: [AVMetadataObject],
        from connection: AVCaptureConnection
    ) {
        guard let object = objects.first as? AVMetadataMachineReadableCodeObject,
              let value = object.stringValue
        else { return }
        MainActor.assumeIsolated { received(value) }
    }

    private func received(_ value: String) {
        // The camera sees the same code thirty times a second. The
        // first one is the only one that means anything.
        guard !found else { return }
        found = true

        // Stop here rather than in viewDidDisappear. The next thing
        // this app does is activate an audio session, and a capture
        // session still tearing down while that happens is how the
        // camera ends up reporting Fig errors into the log.
        let running = Running(session: session)
        Task.detached { running.stop() }

        UINotificationFeedbackGenerator().notificationOccurred(.success)
        onFound?(value)
    }
}

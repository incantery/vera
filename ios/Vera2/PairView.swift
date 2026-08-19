import AVFoundation
import SwiftUI

// Pairing: point the phone at the Mac.

struct PairView: View {
    let onPaired: (Pairing) -> Void

    @State private var scanning = false
    @State private var problem: String?

    var body: some View {
        ZStack {
            N.bg.ignoresSafeArea()

            if scanning {
                QRScanner { scanned in
                    guard let pairing = Pairing.decode(scanned) else {
                        problem = "That isn't a Vera pairing code."
                        scanning = false
                        return
                    }
                    scanning = false
                    onPaired(pairing)
                }
                .ignoresSafeArea()

                VStack {
                    Spacer()
                    Text("Point at the code on your Mac")
                        .font(N.body(14))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 18)
                        .padding(.vertical, 11)
                        .background(.black.opacity(0.55), in: Capsule())
                        .padding(.bottom, 60)
                }
            } else {
                VStack(spacing: 0) {
                    Spacer()

                    Text("Vera")
                        .font(N.body(30, .semibold))
                        .foregroundStyle(N.text)

                    Text("Vera runs on your Mac. Start it there, open the page it prints, and scan the code.")
                        .font(N.body(14))
                        .leading(14, 1.55)
                        .foregroundStyle(N.dim)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 44)
                        .padding(.top, 12)

                    Text("vera2 --addr :4780")
                        .font(N.mono(12))
                        .foregroundStyle(N.dim)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 9)
                        .background(N.surface, in: RoundedRectangle(cornerRadius: 8))
                        .padding(.top, 22)

                    if let problem {
                        Text(problem)
                            .font(N.body(13))
                            .foregroundStyle(N.accent300)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 44)
                            .padding(.top, 20)
                    }

                    Spacer()

                    Button {
                        Task { await beginScanning() }
                    } label: {
                        Text("Scan the code")
                            .font(N.body(15, .medium))
                            .foregroundStyle(N.accent300)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 15)
                            .overlay {
                                RoundedRectangle(cornerRadius: 12)
                                    .strokeBorder(N.accent, lineWidth: 1)
                            }
                    }
                    .buttonStyle(.plain)
                    .padding(.horizontal, 32)
                    .padding(.bottom, 40)
                }
            }
        }
    }

    private func beginScanning() async {
        problem = nil
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            scanning = true
        case .notDetermined:
            scanning = await AVCaptureDevice.requestAccess(for: .video)
        default:
            problem = "Vera can't use the camera. Settings → Vera2."
        }
    }
}

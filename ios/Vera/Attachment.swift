import Foundation
import UIKit
import UniformTypeIdentifiers

// Pictures, on their way to the Mac.
//
// Vera has no eyes: the model she runs on takes text. What she has is
// somebody to hand a picture to — Claude Code, as a delegate or in a
// fleet room, reads images off the disk. So a screenshot sent from
// here is kept once on the Mac and travels onward as a file path.
//
// Which leaves this side one job, and one wrinkle. The job is to get
// the bytes onto the wire. The wrinkle is that a phone's own pictures
// are frequently HEIC, which nothing downstream reads — so anything
// that is not already one of the four formats the Mac keeps is
// re-encoded here, where the picture still exists as pixels.

/// One picture as `POST /say` carries it — the wire shape of Go's
/// `attach.Image`. Base64 rather than a second request because the
/// words and the picture are one message.
struct SayImage: Encodable, Sendable, Equatable {
    var name: String?
    var mime: String?
    var data: String
}

enum Attachment {
    /// The same ceiling the Mac enforces. Checked here so a photo that
    /// is too big is shrunk — or refused — while it is still on this
    /// side, rather than after it has crossed a hotel's wifi.
    static let maxBytes = 16 << 20

    /// The four the Mac keeps, by the bytes they start with.
    ///
    /// Sniffed rather than taken from the picker's own claim about the
    /// file: what matters is what the Mac will make of it, and the Mac
    /// sniffs too. nil means "re-encode this".
    static func kind(of data: Data) -> String? {
        func starts(_ bytes: [UInt8], at offset: Int = 0) -> Bool {
            guard data.count >= offset + bytes.count else { return false }
            let start = data.index(data.startIndex, offsetBy: offset)
            return Array(data[start..<data.index(start, offsetBy: bytes.count)]) == bytes
        }
        if starts([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]) { return "image/png" }
        if starts([0xFF, 0xD8, 0xFF]) { return "image/jpeg" }
        if starts(Array("GIF87a".utf8)) || starts(Array("GIF89a".utf8)) { return "image/gif" }
        if starts(Array("RIFF".utf8)), starts(Array("WEBP".utf8), at: 8) { return "image/webp" }
        return nil
    }

    /// One picture, ready to send, or nil if nothing usable can be
    /// made of it.
    ///
    /// A screenshot arrives as a PNG and is passed through untouched —
    /// re-encoding text would only blur it. A photo from the library
    /// arrives as HEIC and becomes a JPEG, shrinking until it fits,
    /// because a picture the Mac will refuse is worse than a slightly
    /// smaller one.
    static func image(_ data: Data, named name: String) -> SayImage? {
        if let kind = kind(of: data), data.count <= maxBytes {
            return SayImage(name: name, mime: kind, data: data.base64EncodedString())
        }
        guard let picture = UIImage(data: data) else { return nil }
        guard let fitted = fit(picture) else { return nil }
        return SayImage(name: rename(name, as: "jpg"), mime: "image/jpeg", data: fitted.base64EncodedString())
    }

    /// JPEG bytes under the ceiling, by quality first and then by size.
    /// Quality first because halving the pixels of a screenshot loses
    /// the words in it, and there is nothing else worth attaching a
    /// screenshot for.
    static func fit(_ picture: UIImage) -> Data? {
        for quality in [0.9, 0.7, 0.5] as [CGFloat] {
            if let data = picture.jpegData(compressionQuality: quality), data.count <= maxBytes {
                return data
            }
        }
        var scaled = picture
        for _ in 0..<4 {
            guard let smaller = shrink(scaled, by: 0.6) else { return nil }
            scaled = smaller
            if let data = scaled.jpegData(compressionQuality: 0.7), data.count <= maxBytes {
                return data
            }
        }
        return nil
    }

    private static func shrink(_ picture: UIImage, by factor: CGFloat) -> UIImage? {
        let size = CGSize(width: picture.size.width * factor, height: picture.size.height * factor)
        guard size.width >= 1, size.height >= 1 else { return nil }
        return UIGraphicsImageRenderer(size: size).image { _ in
            picture.draw(in: CGRect(origin: .zero, size: size))
        }
    }

    /// The name with its extension told the truth about the re-encode.
    static func rename(_ name: String, as ext: String) -> String {
        let base = (name as NSString).deletingPathExtension
        return (base.isEmpty ? "image" : base) + "." + ext
    }

    /// Whether there is a picture on the pasteboard, without reading
    /// it. `hasImages` is the cheap question, and on a recent iOS it is
    /// also the one that does not raise the paste banner.
    @MainActor
    static func hasImage(_ board: UIPasteboard = .general) -> Bool { board.hasImages }

    /// What is on the pasteboard right now, if it is a picture.
    @MainActor
    static func fromPasteboard(_ board: UIPasteboard = .general) -> SayImage? {
        // The raw bytes first: a screenshot copied from another app is
        // already a PNG, and going through UIImage would re-encode it
        // for nothing.
        if let png = board.data(forPasteboardType: UTType.png.identifier) {
            return image(png, named: "pasted.png")
        }
        if let picture = board.image, let png = picture.pngData() {
            return image(png, named: "pasted.png")
        }
        return nil
    }

    /// How the composer says what is going with the message.
    static func summary(_ images: [SayImage]) -> String {
        switch images.count {
        case 0: ""
        case 1: "1 image"
        default: "\(images.count) images"
        }
    }
}

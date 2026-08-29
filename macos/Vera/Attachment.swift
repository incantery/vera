import AppKit
import Foundation

// Pictures, on their way to Vera Core.
//
// Vera has no eyes: the model she runs on takes text. What she has is
// somebody to hand a picture to — Claude Code, as a delegate or in a
// fleet room, reads images off the disk. So a screenshot pasted here
// is kept once on the Mac and travels onward as a file path.
//
// Which means this side's whole job is: get the bytes, make them a
// PNG, and send them. Everything after that is Core's.

/// One picture as `POST /say` carries it — the wire shape of Go's
/// `attach.Image`. Base64 rather than multipart because /say is one
/// JSON body and a picture is not a separate request: the words and
/// the picture are one message.
struct SayImage: Encodable, Sendable, Equatable {
    var name: String?
    var mime: String?
    var data: String
}

enum Attachment {
    /// The same ceiling Core enforces. Checked here so a picture that
    /// is too big is refused while it is still on this side, rather
    /// than after it has been encoded and sent.
    static let maxBytes = 16 << 20

    /// PNG bytes for whatever an image pasteboard is holding.
    ///
    /// PNG first because most things put one there; TIFF converted
    /// because several apps — Preview, Finder, older screenshot paths
    /// — put only that. Everything downstream expects one of four
    /// formats, and converting here means Core never has to.
    static func png(png: Data?, tiff: Data?) -> Data? {
        if let png, !png.isEmpty { return png }
        guard let tiff, !tiff.isEmpty, let rep = NSBitmapImageRep(data: tiff) else { return nil }
        return rep.representation(using: .png, properties: [:])
    }

    /// One picture, ready to send, or nil if there is nothing usable
    /// there. Empty and oversized are both nil: a message that quietly
    /// carried no bytes would be worse than one that says it could not.
    static func image(_ data: Data, named name: String) -> SayImage? {
        guard !data.isEmpty, data.count <= maxBytes else { return nil }
        return SayImage(name: name, mime: "image/png", data: data.base64EncodedString())
    }

    /// What is on the pasteboard right now, if it is a picture.
    ///
    /// Read at the moment ⌘V is pressed rather than at send time: a
    /// person who pastes and then copies something else meant the
    /// first one.
    @MainActor
    static func fromPasteboard(_ board: NSPasteboard = .general) -> SayImage? {
        guard let bytes = png(png: board.data(forType: .png), tiff: board.data(forType: .tiff)) else { return nil }
        return image(bytes, named: "pasted")
    }

    /// Whether there is a picture to take, without taking it — what
    /// decides whether ⌘V attaches or pastes text as usual.
    @MainActor
    static func hasImage(_ board: NSPasteboard = .general) -> Bool {
        board.canReadItem(withDataConformingToTypes: [
            NSPasteboard.PasteboardType.png.rawValue,
            NSPasteboard.PasteboardType.tiff.rawValue,
        ])
    }

    /// How the panel says what is attached. Named counts rather than a
    /// list: they are all called "pasted", and what a person needs to
    /// know is that something is going with the question.
    static func summary(_ images: [SayImage]) -> String {
        switch images.count {
        case 0: ""
        case 1: "1 image attached"
        default: "\(images.count) images attached"
        }
    }
}

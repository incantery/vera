import ImageIO
import UIKit
import UniformTypeIdentifiers
import XCTest
@testable import Vera

// Pictures on their way to the Mac.
//
// The rule this is protecting: what already is one of the four formats
// the Mac keeps goes across untouched, and anything else — a phone's
// own HEIC, most of all — is turned into something that is, here,
// while it still exists as pixels. A picture the Mac refuses is a
// question about nothing.

final class AttachmentTests: XCTestCase {

    private func png(_ side: Int = 4) -> Data {
        let size = CGSize(width: side, height: side)
        return UIGraphicsImageRenderer(size: size).pngData { ctx in
            UIColor.systemRed.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
        }
    }

    /// A TIFF: readable by UIImage, and not one of the four the Mac
    /// keeps — the same shape as the HEIC a photo library hands over,
    /// which the simulator cannot be relied on to produce.
    private func tiff(_ side: Int = 4) -> Data? {
        let size = CGSize(width: side, height: side)
        let image = UIGraphicsImageRenderer(size: size).image { ctx in
            UIColor.systemGreen.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
        }
        guard let cg = image.cgImage else { return nil }
        let out = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(out, UTType.tiff.identifier as CFString, 1, nil)
        else { return nil }
        CGImageDestinationAddImage(destination, cg, nil)
        guard CGImageDestinationFinalize(destination) else { return nil }
        return out as Data
    }

    private func jpeg(_ side: Int = 4) -> Data {
        let size = CGSize(width: side, height: side)
        let image = UIGraphicsImageRenderer(size: size).image { ctx in
            UIColor.systemBlue.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
        }
        return image.jpegData(compressionQuality: 0.9)!
    }

    func testTheFourFormatsAreRecognisedByTheirBytes() {
        XCTAssertEqual(Attachment.kind(of: png()), "image/png")
        XCTAssertEqual(Attachment.kind(of: jpeg()), "image/jpeg")
        XCTAssertEqual(Attachment.kind(of: Data("GIF89a and then some".utf8)), "image/gif")

        var webp = Data("RIFF".utf8)
        webp.append(contentsOf: [0, 0, 0, 0])
        webp.append(Data("WEBPVP8 ".utf8))
        XCTAssertEqual(Attachment.kind(of: webp), "image/webp")
    }

    func testWhatIsNotAPictureIsNotOneOfThem() {
        XCTAssertNil(Attachment.kind(of: Data()))
        XCTAssertNil(Attachment.kind(of: Data("this is just some prose".utf8)))
        XCTAssertNil(Attachment.kind(of: Data([0x89, 0x50])), "a truncated header is not a PNG")
        // "RIFF" alone is a WAV as often as a WebP.
        XCTAssertNil(Attachment.kind(of: Data("RIFF....WAVEfmt ".utf8)))
    }

    // A screenshot is already a PNG. Re-encoding it would only blur the
    // text, which is the whole reason it was sent.
    func testAScreenshotGoesAcrossUntouched() throws {
        let bytes = png()
        let image = try XCTUnwrap(Attachment.image(bytes, named: "shot.png"))
        XCTAssertEqual(image.mime, "image/png")
        XCTAssertEqual(image.name, "shot.png")
        XCTAssertEqual(Data(base64Encoded: image.data), bytes)
    }

    // What the phone's own library hands over is HEIC, which nothing
    // downstream reads. It becomes a JPEG, and says so.
    func testSomethingInNoFormatTheMacKeepsIsReencoded() throws {
        let bytes = try XCTUnwrap(tiff())
        XCTAssertNil(Attachment.kind(of: bytes), "the test's premise: a TIFF is not one of the four")
        XCTAssertNotNil(UIImage(data: bytes), "the test's premise: UIImage can still read it")

        let image = try XCTUnwrap(Attachment.image(bytes, named: "IMG_0042.HEIC"))
        XCTAssertEqual(image.mime, "image/jpeg")
        XCTAssertEqual(image.name, "IMG_0042.jpg", "the name has to stop claiming to be HEIC")
        let sent = try XCTUnwrap(Data(base64Encoded: image.data))
        XCTAssertEqual(Attachment.kind(of: sent), "image/jpeg")
    }

    func testWhatCannotBeMadeIntoAPictureIsRefused() {
        XCTAssertNil(Attachment.image(Data(), named: "empty"))
        XCTAssertNil(Attachment.image(Data("this is just some prose".utf8), named: "notes.txt"))
    }

    func testRename() {
        XCTAssertEqual(Attachment.rename("IMG_0042.HEIC", as: "jpg"), "IMG_0042.jpg")
        XCTAssertEqual(Attachment.rename("no extension", as: "jpg"), "no extension.jpg")
        XCTAssertEqual(Attachment.rename("", as: "jpg"), "image.jpg")
    }

    func testSummary() {
        XCTAssertEqual(Attachment.summary([]), "")
        let one = SayImage(name: "a", mime: "image/png", data: "")
        XCTAssertEqual(Attachment.summary([one]), "1 image")
        XCTAssertEqual(Attachment.summary([one, one]), "2 images")
    }

    // The transcript keeps a count, not the bytes, and a transcript
    // written before pictures existed still reads.
    func testAnExchangeRemembersHowManyPicturesWentWithIt() throws {
        let with = Exchange(said: "what is wrong here", images: 2)
        let round = try JSONDecoder().decode(Exchange.self, from: JSONEncoder().encode(with))
        XCTAssertEqual(round.images, 2)

        let old = Data(#"{"id":"\#(UUID().uuidString)","said":"hello","steps":[],"done":true,"seen":0}"#.utf8)
        let before = try JSONDecoder().decode(Exchange.self, from: old)
        XCTAssertNil(before.images, "a transcript from before pictures must still decode")
    }
}

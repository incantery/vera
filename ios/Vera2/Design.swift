import SwiftUI

// Nocturne, only the parts this app actually draws. The prototype's
// Design/Nocturne.swift is the full transcription; copying all of it
// here would be importing a vocabulary before there is anything to say
// with it.
enum N {
    static let bg = Color(red: 0.086, green: 0.094, blue: 0.149)      // #161826
    static let surface = Color(red: 0.137, green: 0.145, blue: 0.196) // #232532
    static let text = Color(red: 0.914, green: 0.914, blue: 0.929)    // #E9E9ED
    static let dim = Color(red: 0.545, green: 0.553, blue: 0.608)     // #8B8D9B
    static let accent = Color(red: 0.569, green: 0.518, blue: 0.851)  // #9184D9
    static let accent300 = Color(red: 0.824, green: 0.808, blue: 0.992) // #D2CEFD

    static func body(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .system(size: size, weight: weight, design: .default)
    }
    static func mono(_ size: CGFloat) -> Font {
        .system(size: size, weight: .regular, design: .monospaced)
    }
}

extension View {
    /// CSS-style line height, which the design system is written in.
    func leading(_ size: CGFloat, _ multiple: CGFloat) -> some View {
        lineSpacing(size * (multiple - 1))
    }
}

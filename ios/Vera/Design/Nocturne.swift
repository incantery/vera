import SwiftUI
import UIKit

// Nocturne — the design system's tokens, transcribed from
// _ds/nocturne-910b80e9.../styles.css plus the in-between values the
// v5 walkthrough uses directly. This file is the only place a raw hex
// belongs; every other file names a token.

extension Color {
    init(hex: UInt32, opacity: Double = 1) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: opacity
        )
    }
}

enum Nocturne {

    // — ground —
    static let bg = Color(hex: 0x161826)
    static let surface = Color(hex: 0x232532)
    static let text = Color(hex: 0xE9E9ED)
    static let accent = Color(hex: 0x9184D9)
    static let accent2 = Color(hex: 0xA7A1DB)

    // — neutral ramp —
    static let neutral100 = Color(hex: 0xF3F5FE)
    static let neutral200 = Color(hex: 0xE4E7F5)
    static let neutral300 = Color(hex: 0xCFD3E5)
    static let neutral400 = Color(hex: 0xB2B6CA)
    static let neutral500 = Color(hex: 0x9397AB)
    static let neutral600 = Color(hex: 0x75798C)
    static let neutral700 = Color(hex: 0x595D6C)
    static let neutral800 = Color(hex: 0x3F424D)
    static let neutral900 = Color(hex: 0x292B31)

    // — accent ramp —
    static let accent100 = Color(hex: 0xF5F4FF)
    static let accent200 = Color(hex: 0xE7E5FE)
    static let accent300 = Color(hex: 0xD2CEFD)
    static let accent400 = Color(hex: 0xB5ABFC)
    static let accent500 = Color(hex: 0x968AE0)
    static let accent600 = Color(hex: 0x796CBF)
    static let accent700 = Color(hex: 0x5D5294)
    static let accent800 = Color(hex: 0x423A6A)
    static let accent900 = Color(hex: 0x2B2741)

    // — roles the walkthrough leans on —
    /// Emphasised body copy inside a muted line.
    static let bright = Color(hex: 0xC3C6D4)
    /// Vera's voice: quieter than the user's, still fully readable.
    static let body = Color(hex: 0xA6AABD)
    /// Secondary copy.
    static let soft = neutral500
    /// Tertiary copy, section labels, provenance footnotes.
    static let dim = Color(hex: 0x787D92)
    /// Superseded text — struck, not deleted.
    static let faint = Color(hex: 0x4A4D5C)
    /// Hairline between rows inside a container.
    static let rule = Color(hex: 0x1D1F2B)
    /// The low end of a bar chart's value ramp.
    static let barLow = Color(hex: 0x2B2D3A)
    static let barMid = neutral800
    static let barHigh = neutral700

    // — radii —
    static let radiusSm: CGFloat = 4
    static let radiusMd: CGFloat = 8
    static let radiusLg: CGFloat = 14
    static let radiusPill: CGFloat = 999
}

// MARK: - Elevation
//
// The dark theme spends a hairline edge before it spends a shadow:
//   --shadow-sm: 0 0 0 1px #3f424d
//   --shadow-md: 0 0 0 1px #595d6c, 0 6px 18px rgba(0,0,0,0.55)
//   --shadow-lg: 0 0 0 1px #9397ab, 0 16px 40px rgba(0,0,0,0.65)

enum Elevation {
    case sm, md, lg

    var edge: Color {
        switch self {
        case .sm: Nocturne.neutral800
        case .md: Nocturne.neutral700
        case .lg: Nocturne.neutral500
        }
    }

    var ambient: (color: Color, radius: CGFloat, y: CGFloat)? {
        switch self {
        case .sm: nil
        case .md: (.black.opacity(0.55), 9, 6)
        case .lg: (.black.opacity(0.65), 20, 16)
        }
    }
}

private struct ElevationModifier: ViewModifier {
    let level: Elevation
    let radius: CGFloat

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        let shadowed = Group {
            if let a = level.ambient {
                content.shadow(color: a.color, radius: a.radius, y: a.y)
            } else {
                content
            }
        }
        return shadowed.overlay(shape.strokeBorder(level.edge, lineWidth: 1))
    }
}

extension View {
    func elevation(_ level: Elevation, radius: CGFloat = Nocturne.radiusMd) -> some View {
        modifier(ElevationModifier(level: level, radius: radius))
    }

    /// Surface fill + elevation, the pairing that makes a container.
    /// "A card must earn its container" — reach for this only when
    /// something is a real object, never to group loose copy.
    func surfaceCard(radius: CGFloat = Nocturne.radiusMd, elevation level: Elevation = .sm) -> some View {
        background(Nocturne.surface, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .elevation(level, radius: radius)
    }
}

// MARK: - Type
//
// The system is drawn in Inter. If Inter is present in the bundle we use
// it; otherwise SF stands in at the same sizes and weights, which is the
// closest grotesque iOS ships. Dropping Inter*.ttf into the target is the
// only step needed to upgrade fidelity — no code changes.

enum VeraFont {
    private static let hasInter = UIFont(name: "Inter", size: 12) != nil
        || UIFont(name: "Inter-Regular", size: 12) != nil

    static func heading(_ size: CGFloat, _ weight: Font.Weight = .medium) -> Font {
        resolved(size, weight)
    }

    static func body(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        resolved(size, weight)
    }

    static func mono(_ size: CGFloat, _ weight: Font.Weight = .medium) -> Font {
        .system(size: size, weight: weight, design: .monospaced)
    }

    private static func resolved(_ size: CGFloat, _ weight: Font.Weight) -> Font {
        hasInter
            ? .custom("Inter", size: size).weight(weight)
            : .system(size: size, weight: weight)
    }
}

extension View {
    /// CSS-style line-height. SwiftUI stacks lines at roughly 1.2em by
    /// default, so a `1.55` multiple asks for the remaining 0.35em.
    func leading(_ size: CGFloat, _ multiple: CGFloat) -> some View {
        lineSpacing(max(0, size * (multiple - 1.2)))
    }
}

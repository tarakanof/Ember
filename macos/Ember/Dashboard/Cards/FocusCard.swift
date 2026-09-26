import SwiftUI
import EmberKit

/// Card 2: today against the goal, or the running phase, plus the streak
/// and completion numbers.
struct FocusCard: View {
    let stats: Loadable<PomoStats>
    let pomodoro: Loadable<PomoState>
    var config: PomoConfig?
    var now = Date()

    var body: some View {
        let feed = model()
        DashboardCard(title: "Focus", systemImage: "timer") {
            FeedStateView(feed: feed, placeholder: Self.placeholder,
                          isEmpty: \.isEmpty,
                          emptyTitle: "No focus sessions yet — click Start Focus to begin",
                          emptySymbol: "timer",
                          offTitle: "Pomodoro is off",
                          offDescription: "Turn it on in Settings › Focus.",
                          offSettingsPane: "focus") { summary in
                content(summary)
            }
        }
    }

    /// The timer state wins over stats for the off state: both are 404 when
    /// Pomodoro is off, but the timer loads first.
    private func model() -> Loadable<FocusSummary> {
        if pomodoro.error == .featureOff || stats.error == .featureOff {
            return .failed(.featureOff, last: nil, lastAt: nil)
        }
        if stats.isLoading {
            // A running timer is worth showing before the numbers arrive.
            guard let p = pomodoro.value, p.mode != .idle else { return .loading }
            return .loaded(FocusSummary(stats: nil, state: p, now: now), at: now)
        }
        return stats.map { FocusSummary(stats: $0, state: pomodoro.value, now: now) }
    }

    private func content(_ s: FocusSummary) -> some View {
        HStack(alignment: .center, spacing: 20) {
            ring(s)
            VStack(alignment: .leading, spacing: 10) {
                headline(s)
                Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 6) {
                    GridRow {
                        fact("Focus today", DurationText.minutes(s.focusMinutesToday))
                        fact("Streak", streakText(s))
                    }
                    GridRow {
                        fact("Completed", s.completionRate.map { Percent.text(ratio: $0) } ?? "—",
                             help: "Focus sessions finished rather than stopped or skipped, last 30 days")
                        fact("Best streak", s.longestStreak.formatted())
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxHeight: .infinity)
    }

    @ViewBuilder
    private func ring(_ s: FocusSummary) -> some View {
        let g = s.gauge
        Gauge(value: g.value, in: 0...g.total) {
            Text("Focus")
        } currentValueLabel: {
            switch s.ring {
            case .goal(let done, let goal):
                VStack(spacing: 0) {
                    Text(done.formatted()).font(.title.weight(.semibold))
                    if let goal { Text("of \(goal)").font(.caption).foregroundStyle(.secondary) }
                }
            case .phase(_, let remaining, _, let endsAt, _):
                Group {
                    if let endsAt {
                        Text(timerInterval: now...max(now, endsAt), countsDown: true)
                    } else {
                        Text(DurationText.remaining(remaining))
                    }
                }
                .font(.title3.weight(.semibold))
                .monospacedDigit()
            }
        }
        .gaugeStyle(RingGaugeStyle(tint: ringTint(s), lineWidth: 10))
        .frame(width: 118, height: 118)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(ringLabel(s))
        .accessibilityValue(ringValue(s))
    }

    @ViewBuilder
    private func headline(_ s: FocusSummary) -> some View {
        switch s.ring {
        case .phase(let phase, _, _, let endsAt, let round):
            VStack(alignment: .leading, spacing: 2) {
                Text(phase.displayName).font(.title3.weight(.semibold))
                Text(endsAt == nil ? "Paused · round \(round)" : "Round \(round)")
                    .font(.callout).foregroundStyle(.secondary)
            }
        case .goal(let done, let goal):
            VStack(alignment: .leading, spacing: 2) {
                if let goal {
                    Text(s.dailyGoalMet ? "Daily goal met" : "\(max(0, goal - done)) to go today")
                        .font(.title3.weight(.semibold))
                    Text("Goal: ^[\(goal) session](inflect: true) a day").font(.callout).foregroundStyle(.secondary)
                } else {
                    Text("^[\(done) session](inflect: true) today").font(.title3.weight(.semibold))
                    Text("No daily goal set").font(.callout).foregroundStyle(.secondary)
                }
            }
        }
    }

    private func fact(_ title: LocalizedStringKey, _ value: String, help: LocalizedStringKey? = nil) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(value).font(.body.weight(.medium)).monospacedDigit()
            Text(title).font(.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue(value)
        .help(help ?? "")
    }

    private func streakText(_ s: FocusSummary) -> String {
        inflected("^[\(s.streak) day](inflect: true)")
    }

    private func ringTint(_ s: FocusSummary) -> Color {
        switch s.ring {
        case .phase(let phase, _, _, _, _): EmberColors.phase(phase, config: config)
        case .goal: s.dailyGoalMet ? .green : .accentColor
        }
    }

    private func ringLabel(_ s: FocusSummary) -> Text {
        switch s.ring {
        case .phase(let phase, _, _, _, _): Text(phase.displayName)
        case .goal: Text("Focus sessions today")
        }
    }

    private func ringValue(_ s: FocusSummary) -> Text {
        switch s.ring {
        case .phase(_, let remaining, _, _, _): Text("\(DurationText.remaining(remaining)) left")
        case .goal(let done, let goal?): Text("\(done) of \(goal)")
        case .goal(let done, nil): Text(done.formatted())
        }
    }

    static let placeholder = FocusSummary(
        stats: PomoStats(today: PomoDayStat.placeholder, history: [], streak: 3, longestStreak: 9,
                         goal: GoalStatus(dailySessions: 8, todayCompleted: 3)),
        state: nil, now: .now)
}

/// A ring gauge sized by its frame, for the Focus card: the stock
/// `.accessoryCircularCapacity` is watch-complication small on the Mac.
struct RingGaugeStyle: GaugeStyle {
    var tint: Color
    var lineWidth: CGFloat

    func makeBody(configuration: Configuration) -> some View {
        Ring(value: configuration.value, tint: tint, lineWidth: lineWidth) {
            configuration.currentValueLabel
        }
    }

    private struct Ring<Label: View>: View {
        let value: Double
        let tint: Color
        let lineWidth: CGFloat
        @ViewBuilder let label: Label
        @Environment(\.redactionReasons) private var redaction
        @Environment(\.accessibilityReduceMotion) private var reduceMotion

        var body: some View {
            ZStack {
                Circle()
                    .stroke(.quaternary, lineWidth: lineWidth)
                Circle()
                    .trim(from: 0, to: value)
                    .stroke(redaction.isEmpty ? AnyShapeStyle(tint) : AnyShapeStyle(.quaternary),
                            style: StrokeStyle(lineWidth: lineWidth, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                    .animation(reduceMotion ? nil : .smooth, value: value)
                label
            }
            .padding(lineWidth / 2)
        }
    }
}

extension PomoDayStat {
    /// Shape-only sample for redacted loading states.
    static let placeholder = try! JSONDecoder().decode(
        PomoDayStat.self, from: Data(#"{"date":"2026-01-01","completed_focus":3,"focus_min":75}"#.utf8))
}

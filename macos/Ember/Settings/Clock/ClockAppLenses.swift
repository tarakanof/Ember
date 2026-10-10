import SwiftUI
import EmberKit

extension AppEnvironment {
    var clockAgents: ClockAppLens<ClockConfig.Agents> {
        ClockAppLens(source: settings.usage, clock: clockConfig, slice: \.agents)
    }

    var clockFocus: ClockAppLens<ClockConfig.Focus> {
        ClockAppLens(source: settings.pomodoro, clock: clockConfig, slice: \.focus)
    }

    var clockWeather: ClockAppLens<ClockConfig.Weather> {
        ClockAppLens(source: settings.weather, clock: clockConfig, slice: \.weather)
    }

    var clockCalendar: ClockAppLens<ClockConfig.Calendar> {
        ClockAppLens(source: settings.meetings, clock: clockConfig, slice: \.calendar)
    }
}

extension ClockAppLens {
    var binding: Binding<Slice.Source> {
        Binding(get: { draft }, set: { draft = $0 })
    }
}

extension View {
    func autosaves<S: ClockAppSlice>(_ lens: ClockAppLens<S>) -> some View {
        autosaves(lens.source).autosaves(lens.clock.config)
    }
}

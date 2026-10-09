import Foundation

public enum SettingsInfo: String, CaseIterable, Sendable {
    case knobPoll, knobStatsInterval, knobLiveInterval, knobFastLink
    case knobFollowBrightness, knobMinBrightness, knobStartupBrightness
    case knobSleepy, knobSourceLabel, knobWorkingRing
    case knobQuietCalm, knobQuietDim
    case clockAutoBrightness, clockUppercase
    case clockTimePerApp, clockAutoTransition, clockScrollSpeed, clockBlockNavigation
    case melody, attentionChime, limitAlarm
    case bottomBar, activityTrail
    case weatherNativeTileIcon, weatherMoonPhase, weatherOverlay, weatherPopupInterval, weatherNativePopupIcons
    case meetingPopupLead, reminderNativeIcon
    case codexIncludeClaude, codexSources, codexAppServer
    case claudeAgentsPoll, doneTTL, statuslineTimeout
    case focusAutoStart, focusStopAfter, focusWeeklyGoal
    case weatherProvider, weatherSevereAlert
    case sourceColor, menuBarIcon

    public var summary: LocalizedStringResource {
        switch self {
        case .knobPoll: "How often the knob asks Ember for updates"
        case .knobStatsInterval: "How often the knob uploads its hardware stats"
        case .knobLiveInterval: "How often the knob reports while its Hardware page is open"
        case .knobFastLink: "Runs the knob's screen link at 80 MHz instead of 40 MHz"
        case .knobFollowBrightness: "Matches the brightness Ember works out for displays without a light sensor"
        case .knobMinBrightness: "The dimmest the knob ever gets"
        case .knobStartupBrightness: "The level the screen lights at when the knob starts"
        case .knobSleepy: "Idle time before the bot gets sleepy"
        case .knobSourceLabel: "Writes the agent's computer name along the bottom of the face"
        case .knobWorkingRing: "A glint circles the bot's outline while an agent works"
        case .knobQuietCalm: "Keeps the bot calm during quiet hours"
        case .knobQuietDim: "The brightest the knob gets during quiet hours"
        case .clockAutoBrightness: "Lets the clock's light sensor set its brightness"
        case .clockUppercase: "Shows the text of apps and popups in capitals"
        case .clockTimePerApp: "How long each app stays on screen"
        case .clockAutoTransition: "Moves on to the next app by itself"
        case .clockScrollSpeed: "How fast text that doesn't fit moves across the panel"
        case .clockBlockNavigation: "Stops the clock's buttons from switching apps"
        case .melody: "The tune the clock plays"
        case .attentionChime: "Chimes when an agent starts waiting for you or fails"
        case .limitAlarm: "Tells you when a used-up 5-hour limit resets"
        case .bottomBar: "What the bottom row of each agent card shows"
        case .activityTrail: "Keeps recent tool calls in the activity card"
        case .weatherNativeTileIcon: "Uses an animated icon on the conditions tile"
        case .weatherMoonPhase: "Shows the moon's phase on clear nights"
        case .weatherOverlay: "Animates rain or snow over the weather tile and popups"
        case .weatherPopupInterval: "Repeats the weather popup on a schedule"
        case .weatherNativePopupIcons: "Uses animated icons in weather popups"
        case .meetingPopupLead: "A popup shortly before each meeting"
        case .reminderNativeIcon: "Shows an icon stored on the clock instead of the bell"
        case .codexIncludeClaude: "Also shows Codex runs that Claude Code's Codex plugin starts"
        case .codexSources: "Which kinds of Codex sessions report"
        case .codexAppServer: "Follows Codex TUI sessions through the app-server daemon"
        case .claudeAgentsPoll: "Checks sessions against claude agents to catch missed changes"
        case .doneTTL: "How long a finished session keeps reporting"
        case .statuslineTimeout: "How long your own status line command may run"
        case .focusAutoStart: "Runs breaks and focus phases back to back"
        case .focusStopAfter: "Stops the whole Pomodoro after this long"
        case .focusWeeklyGoal: "Active days to aim for in the last 7"
        case .weatherProvider: "Where the server gets conditions and the forecast"
        case .weatherSevereAlert: "Pops up with a sound when severe weather starts"
        case .sourceColor: "Tints this Mac's agent icon and name on the clock"
        case .menuBarIcon: "The animated bot, or the glyph of the active tool"
        }
    }

    public var detail: LocalizedStringResource {
        switch self {
        case .knobPoll:
            "How often the knob asks Ember for updates when it can't wait on an open request, for example with an older server or after an error. A current server answers the moment something changes, so a shorter interval mostly adds requests. Default: 2 s."
        case .knobStatsInterval:
            "How often the knob sends the stats behind its Hardware page. Shorter gives finer charts, but more requests from the knob and more memory on the server. Default: 1 min."
        case .knobLiveInterval:
            "While the knob's Hardware page is open in Ember, the knob checks in this often so the charts move live, then goes back to normal. The real pace rounds up to the check interval, so 5 s can come out near 6 s. Default: 5 s."
        case .knobFastLink:
            "Redraws the screen faster (a full frame in about 25 ms instead of 35) and halves the time in which a redraw can tear. 80 MHz is beyond the panel's rated speed, so the knob checks the link at startup and every 5 s and drops back to 40 MHz by itself if a check fails. Changing it restarts the knob; default: on."
        case .knobFollowBrightness:
            "The knob has no light sensor, so it uses the level Ember works out from the clock's light sensor, or from sunrise and sunset at the weather location when there's no reading. Off keeps the knob at the Brightness level. Default: on."
        case .knobMinBrightness:
            "The knob never goes below this, whether it follows Ember or not. Raising it above Brightness raises Brightness too. Default: 4 percent."
        case .knobStartupBrightness:
            "The screen stays dark while the knob boots, then lights at this level until your brightness setting takes over. Default: 60 percent."
        case .knobSleepy:
            "After this long with no agent activity, the bot's eyelids droop and it blinks slowly; new activity wakes it. Default: 5 min."
        case .knobSourceLabel:
            "While an agent is working, waiting or in error, the name of its computer curves along the bottom of the bot's face, after the tool's icon. Default: on."
        case .knobWorkingRing:
            "While an agent is working, a glint orbits the bot's outline every 3 s, and after a long stretch the eyes chase it for a few laps. Off keeps the outline still. Default: on."
        case .knobQuietCalm:
            "During quiet hours the bot skips its attention and excited animations, so a waiting or failed agent doesn't light up the room. The knob has no speaker, so quiet hours only change what it shows. Default: on."
        case .knobQuietDim:
            "During quiet hours the knob dims to this level, or stays darker if it already is, even below the minimum brightness. Default: 8 percent, the clock's night level."
        case .clockAutoBrightness:
            "The clock follows its light sensor within its own minimum and maximum, staying dim until the room is properly bright, and eases changes in over about 10 s. The Brightness slider isn't used while this is on. Default: off."
        case .clockUppercase:
            "The clock capitalizes the text of pushed apps and notifications before drawing them; an app can ask for its own case instead. Default: on."
        case .clockTimePerApp:
            "How long the clock shows an app before moving to the next. Apps can ask for their own time: Ember's agent cards and weather tiles stay 6 s. Default: 7 s."
        case .clockAutoTransition:
            "Off keeps the current app on screen until you press a button or Ember switches to something that needs you. A running Pomodoro turns this off until it stops, then puts your setting back. Default: on."
        case .clockScrollSpeed:
            "At 100 percent, text moves 21 pixels a second; above 200 percent it gets hard to read. Default: 100 percent."
        case .clockBlockNavigation:
            "The clock's left and right buttons no longer change apps. A running Pomodoro turns this on so the buttons control the timer, then puts your setting back when it stops. Default: off."
        case .melody:
            "Built-in plays Ember's own tune. Pick a melody stored on the clock, or choose Custom and type a stored melody's name or an RTTTL tune."
        case .attentionChime:
            "The clock chimes once when a session starts waiting for you or hits an error and takes over the screen. Quiet hours mute it."
        case .limitAlarm:
            "When Claude's or Codex's 5-hour window is used up, the clock shows a popup with a chime about a minute after it resets, so you know you can carry on. Default: on."
        case .bottomBar:
            "Session pixels draws one dot per active session, the most urgent first. Rate bar shows how much of the 5-hour limit is used, when the tool reports it. Off leaves the row empty."
        case .activityTrail:
            "The activity card scrolls the last few tool calls, newest first, instead of only the current one. It needs the activity card."
        case .weatherNativeTileIcon:
            "Swaps the drawn condition icon for an animated one from the LaMetric gallery, which the server uploads to the clock. On clear nights the moon phase still shows. Default: off."
        case .weatherMoonPhase:
            "On a clear night the conditions tile shows tonight's moon phase instead of the weather icon. Default: on."
        case .weatherOverlay:
            "While it's raining, snowing or storming, the clock animates it over the conditions tile and weather popups. The previews here don't show it. Default: on."
        case .weatherPopupInterval:
            "Shows the weather popup again once this long has passed since the last one, even if nothing changed. Off keeps only the popups above. Default: 2 h."
        case .weatherNativePopupIcons:
            "Weather popups show animated LaMetric gallery icons instead of the drawn ones. Change the icon for each condition under Native Icon IDs. Default: off."
        case .meetingPopupLead:
            "Shows a popup this many minutes before a meeting starts, with a chime if Meeting popup is on in Clock › Sounds. Default: 2 min."
        case .reminderNativeIcon:
            "Uses the animated icon with this ID from the clock's own icon store. The clock must already have it: Ember uploads only the weather and Pomodoro icons."
        case .codexIncludeClaude:
            "Shows the Codex sessions that Claude Code starts through its Codex plugin, marked via Claude. It's off because the Claude Code session that started them already shows that work. Default: off."
        case .codexSources:
            "CLI is the Codex TUI; VS Code covers the IDE extension and the desktop app. Exec and MCP are runs started by scripts or other agents. Default: CLI and VS Code."
        case .codexAppServer:
            "When the Codex app-server daemon is running, Ember reads TUI sessions from its socket, including waits for approval. Without the daemon this does nothing, and Ember never starts it. Default: on."
        case .claudeAgentsPoll:
            "Asks claude agents for each session's state, which ends a wait you answered in a dialog and catches a turn stopped with Esc, where no hook fires. Needs Claude Code 2.1.288 or later. Default: on."
        case .doneTTL:
            "After a Claude Code session finishes or fails, the heartbeat keeps reporting it this long in case a hook's report was lost. Keep it equal to the server's done_ttl_seconds. Default: 30 s."
        case .statuslineTimeout:
            "When Ember wraps your own Claude Code status line, a command that runs longer is stopped and its last good output is shown. An EMBER_STATUSLINE_TIMEOUT_MS exported in the shell that runs claude wins over this, and Ember can't see it. Default: 10 s."
        case .focusAutoStart:
            "When a phase ends, the next one starts at once. Off leaves the next phase waiting until you start it. Default: off."
        case .focusStopAfter:
            "Stops the timer once this much time has passed since you started it, pauses included. It matters most with automatic phases, which would otherwise run on."
        case .focusWeeklyGoal:
            "A day counts as active when it has at least one completed focus session. The Dashboard compares the last 7 days with this goal."
        case .weatherProvider:
            "Conditions and the hourly forecast come from this service. Air quality always comes from Open-Meteo, because MET Norway has none. Default: Open-Meteo."
        case .weatherSevereAlert:
            "Shows a weather popup with a sound when heavy rain, heavy snow or a thunderstorm begins, once each time. Pick the sound in Clock › Sounds; quiet hours mute it. Default: on."
        case .sourceColor:
            "Colors this Mac's agent icon and its name card on the clock, so you can tell computers apart. Off uses the default colors."
        case .menuBarIcon:
            "The animated bot changes its face with your most urgent session. Tool glyphs shows the icon of the most active tool instead."
        }
    }
}

public enum SettingsInfoRequirement: String, CaseIterable, Sendable {
    case loading, diagnosticsOn, currentConditionsOn, scrolling, weatherOn, meetingsOn, remindersOn
    case focusSoundOn, severeAlertOn, setByEnvironment

    public var text: LocalizedStringResource {
        switch self {
        case .loading: "Available once Ember has loaded this setting."
        case .diagnosticsOn: "Available when Diagnostics is Basic or Full."
        case .currentConditionsOn: "Available when Current conditions is on."
        case .scrolling: "Has no effect while Scrolling is set to Don't scroll."
        case .weatherOn: "Available when weather is on in Sources › Weather."
        case .meetingsOn: "Available when Show next meeting is on in Sources › Calendar."
        case .remindersOn: "Available when Ring the clock for due reminders is on in Sources › Calendar."
        case .focusSoundOn: "Available when Focus phase ends is on."
        case .severeAlertOn: "Available when Severe weather alert is on in Sources › Weather."
        case .setByEnvironment: "An environment variable sets this, and it wins over the setting here."
        }
    }
}

extension SettingsInfo {
    public func popover(rowEnabled: Bool, requirement: SettingsInfoRequirement?) -> [LocalizedStringResource] {
        guard !rowEnabled, let requirement else { return [detail] }
        return [detail, requirement.text]
    }
}

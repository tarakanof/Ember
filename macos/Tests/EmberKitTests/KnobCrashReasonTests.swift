import Testing
@testable import EmberKit

@Suite struct KnobCrashReasonTests {
    @Test func everyFirmwareCrashReasonHasWords() {
        #expect(KnobCrashReason.known == ["panic", "int_wdt", "task_wdt", "wdt", "lvgl_stall", "unknown"])
        for raw in KnobCrashReason.known {
            #expect(KnobCrashReason.label(raw) != raw, "\(raw) shows raw")
        }
    }

    @Test func watchdogsStayApart() {
        #expect(KnobCrashReason.label("int_wdt") != KnobCrashReason.label("task_wdt"))
    }

    @Test func lvglStallHasWords() {
        #expect(KnobCrashReason.label("lvgl_stall") == "Display stall")
    }

    @Test func aReasonFromNewerFirmwareShowsAsReported() {
        #expect(KnobCrashReason.label("stack_overflow") == "stack_overflow")
    }
}

# The menu-bar and Dock bot

The macOS app's default icon is a small black ball with two eyes, drawn in code
and animated. It blinks, glances around and changes expression with the winning
session's state, both in the menu bar and in the Dock. It is modeled on the Grok
Bot icon animation. This doc covers what it does, why the numbers are what they
are, and the macOS quirks that shaped the code.

Settings → App picks it. "Menu-bar icon" switches between the bot and the older
per-tool glyphs, "Menu-bar colour" switches between Colored and Monochrome, and
"Dock & app icon" has a "Bot (animated)" entry.

## Code map

| File | Role |
|---|---|
| `macos/Sources/EmberKit/Bot/BotBehavior.swift` | The animation state machine. Pure and deterministic for a seed, so it is unit-tested. Produces a `BotPose` per frame. |
| `macos/Sources/EmberKit/Bot/BotRenderer.swift` | CoreGraphics drawing of a `BotPose`, with a `BotStyle` for the menu bar and one for the Dock. |
| `macos/Ember/MenuBar/BotAnimator.swift` | The one frame loop. Owns the behavior, publishes `pose` for the menu-bar label, drives the `NSDockTile` view, crossfades the menu-bar colour. |
| `macos/Ember/MenuBar/MenuBarLabel.swift` | Shows the bot image or the tool glyph, per prefs. |
| `macos/Ember/AppEnvironment.swift` | `feedBot()` pushes the winning session's state into the animator. `applyAppIcon` switches the Dock tile. |
| `macos/Tests/EmberKitTests/BotBehaviorTests.swift` | Behavior and renderer tests. |

## Moods

`BotMood(state:)` maps the winning session's state. `sleepy` is never requested,
the bot drifts into it after 5 minutes of `idle`.

| State | Mood | Eyes | Body | Menu-bar colour | Dock badge |
|---|---|---|---|---|---|
| idle | idle | slanted capsules, resting up and right | circle | template (black or white) | none |
| idle for 5 min | sleepy | heavy lids, gaze dropped, slow blinks | circle | template | none |
| running | working | capsules scanning left to right, low, like reading | circle | green | none |
| waiting | waiting | big round eyes on the viewer, a hop every few seconds | circle | amber | amber |
| done | done | happy `^ ^` arcs | circle | blue | blue |
| error | error | angry slits | rounded triangle | red | red |

Colours come from `stateColorRGB`, the same palette the tool glyphs use. With
"Monochrome" every mood renders as a template image, so mood only shows through
shape and eyes.

## How it moves

The goal is an icon that looks alive without looking looped. Every interval is
drawn from a lognormal distribution (a median, a spread and a clamp), because
fixed or uniform timing reads as mechanical within a minute.

### Blinks

People blink about 15 to 20 times a minute and less when concentrating. A blink
closes faster than it opens.

- Interval medians: idle 4.0 s, working 6.0 s, waiting 3.0 s, sleepy 2.8 s,
  error 2.2 s, done 3.2 s. Spread sigma 0.45, clamped to 1 to 12 s. That lands
  around 13 to 20 blinks a minute, and the tests pin idle at 40 to 100 over
  4 minutes and require working to blink less than idle.
- Shape: 75 ms closing (accelerating), 35 ms shut, 150 ms opening
  (decelerating). Sleepy blinks run 2.2 times slower.
- The right eye lags the left by 0 to 20 ms, and 12% of blinks are doubles.
- Large gaze shifts carry a blink 60% of the time, like a head turn does. That
  blink replaces the scheduled one instead of adding to it. Stacking them pushed
  the rate to 25 a minute.

### Gaze

- Saccades (eye darts) take `0.025 + 0.045 × distance` seconds, which gives 25
  to about 130 ms, with a small overshoot (back-out easing).
- Fixations between darts: idle 2.2 s median, working 0.9 s, waiting 2.5 s,
  sleepy 8 s, error 0.9 s, done 2.5 s.
- Where the eyes go depends on mood. Idle splits between the resting
  up-and-right pose, the viewer and random spots. Working steps left to right at
  a slightly low height, then sweeps back. Waiting looks at the viewer 75% of the
  time.
- Follow-through: the body leans a few percent toward the gaze, starting 30 ms
  after the eyes and taking 200 ms.

### Mood changes

A state change should glide, not snap. Over about 0.7 s:

- The eye shape swaps while the lids are shut. The change starts a slower,
  softer blink (1.6 times normal length) and swaps at full closure. If a blink
  is already past closure, a second one is queued, and the swap never happens
  with open eyes.
- The gaze glides to the new mood's target in 0.42 s with ease-in-out instead
  of darting there.
- The body morphs circle to triangle in 0.45 s, point by point on a 96-point
  ring. The Dock badge scales in over 0.35 s with a small overshoot. The body
  pops 10% and settles in 0.4 s.
- In the menu bar the colour crossfades over 0.35 s. Fading to or from the
  template look uses the status bar's actual appearance (white on a dark menu
  bar, black on a light one) as the other end.
- Waking from sleepy adds a double blink.

### Waiting hop

Every 4.5 s median, a 0.62 s hop: crouch (wider and shorter), a stretched rise,
a fall, then a landing squash. Squash pivots on the ground so the ball doesn't
float. The menu bar uses half the Dock's hop height to stay inside its image.

### Reduce motion

With Accessibility → Reduce motion on, the bot keeps blinking but stops darting,
hopping and popping, and morphs happen instantly. Toggling the setting takes
effect immediately, even mid-mood.

## Geometry

Eye proportions come from measuring the reference animation. Body radius is 1:

- Idle eyes are capsules 0.14 wide and 0.38 tall, 0.44 apart, resting up and
  right of centre.
- Eyes lean with the gaze. They stand upright and level when looking at the
  viewer and lean up to 27° ("\") as the gaze turns either way. The right eye
  rises slightly as they lean. A small dead zone keeps glances at the viewer
  level.
- Eyes near the rim of the sphere narrow and move closer together
  (foreshortening), and they are clipped to the body so they never spill off
  the triangle.
- At 16 pt the proportional eyes are too thin, so the menu bar scales them 1.25
  times and adds one Retina pixel of width and height. The Dock adds about two
  to three pixels.

## Sizes

Checked against Apple's Human Interface Guidelines for macOS 26, and still
current for macOS 27 Golden Gate:

- **Menu bar.** The bar is 24 pt tall. A round menu bar extra matches the weight
  of system icons at about 16 pt, and images shouldn't be taller than 22 pt. The
  bot is a 16 pt ball in a 22 × 22 pt image, rendered at 44 × 44 px.
- **Dock and app icon.** macOS 26 and 27 use a 1024 px canvas with an 824 px
  plate that the system rounds. Free-form icons get shrunk or put on a grey
  plate. The Dock bot draws its own light 824/1024 plate with about 22.5% corner
  radius, and the ball fills 62% of the canvas, centred.

## macOS gotchas

These cost real debugging time.

- **`MenuBarExtra` labels don't run `onChange` or `onAppear`.** State never
  reached the bot that way. `AppEnvironment.feedBot()` observes
  `live.winningSession` with `withObservationTracking` and re-arms itself on
  every change. `winningSession` is nil once `/state` has failed 3 polls in a
  row, so an outage sends the bot to idle rather than freezing it mid-mood,
  while a single dropped poll changes nothing.
- **A lazily drawn `NSImage` loses its colour in the menu bar.** An
  `NSImage(size:flipped:drawingHandler:)` came out monochrome even with
  `isTemplate = false`. The bot renders into a `CGContext` bitmap first, and
  that keeps its colour.
- **Setting `applicationIconImage` replaces the Dock tile's `contentView`.** Set
  the image first (Cmd-Tab and Finder read it), then attach the live view, or
  the Dock shows a still picture.
- **The Dock tile is only visible while the app is `.regular`.** Ember is an
  `LSUIElement` agent and only becomes `.regular` while Settings or the
  Dashboard is open. The loop skips Dock drawing otherwise, and `AppDelegate`
  re-applies the icon on each promotion.
- **Template eyes are cut out.** In the menu bar the eyes are cleared from the
  body (`.clear` blend inside a transparency layer), so they show the menu bar
  through them in both the template and coloured looks.

## Performance

The loop only runs while something moves. It renders at 30 fps during blinks,
glances and hops, 60 fps only during the 0.7 s mood morph, and otherwise sleeps
until the next scheduled event (at most 10 s). It stops completely when neither
the menu bar nor a visible Dock tile shows the bot. Measured on an M4 with a
session running: 3.6% average CPU with the bot against 2.8% with the static
glyphs. Most of the cost is SwiftUI re-rendering the `MenuBarExtra` label each
frame, which is why the everyday rate is 30 fps.

## Changing it

- Run `swift test --package-path macos`. The behavior is deterministic per seed,
  so tests step it at a fixed frame rate and check properties such as blink
  rate, eye swaps only behind shut lids, no large per-frame gaze jumps during
  morphs, and the sleepy body staying round.
- To look at frames, compile the two `Bot/*.swift` files with a small
  `main.swift` that renders poses into a `CGContext` and writes PNGs. That needs
  no app build and is the fastest way to judge a change.
- Keep new animation in `BotBehavior` as data on `BotPose`, and keep
  `BotRenderer` a pure function of pose and style. The animator should stay a
  loop and nothing more.

## Credits

The look and state set follow the Grok Bot icon animation
([Benji Taylor's post](https://x.com/benjitaylor/status/2087227155076046995)).
Eye proportions and the ring-morph technique were measured from
[sheng-yh/Reproduce-grok-bot-icon](https://github.com/sheng-yh/Reproduce-grok-bot-icon),
which reproduces x.ai's component. That artwork is © xAI, and none of its point
data is copied here. Timing draws on eye-movement research (blink rate 16 to 20
per minute, 150 to 500 ms blinks, 20 to 200 ms saccades) and animation practice
(blink on head turns, asymmetric blinks, eye darts).

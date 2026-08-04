# @name    ember-boot-ping
# @desc    Tells the Ember server the clock rebooted, so it re-pushes its apps
# @author  ember
# @version 1.0
# @headless true
# @config  url text "Ember boot hook" default="__EMBER_BOOT_URL__" maxlen=96 help="Ember's POST /hooks/awtrix/boot URL"

# Pushed apps live in the clock's RAM only, so a reboot loses every Ember tile
# until the server notices. Ember's own watch loop notices within 30 s; this
# script cuts that to seconds by telling the server itself.
#
# Fire-and-forget by design: an Ember older than the release that added
# /hooks/awtrix/boot answers 404, which is still proof the server heard us, so
# only a transport failure (status 0 — no Wi-Fi yet, server down) retries.

class EmberBootPing
  var url
  var ticks    # loop() calls left before the next attempt
  var tries    # attempts left this boot; 0 = done

  def init()
    self.url = store.get("url")
    self.ticks = 5      # let Wi-Fi and DHCP settle before the first POST
    self.tries = 3
  end

  def loop()
    if self.tries <= 0 return end
    if self.ticks > 0
      self.ticks -= 1
      return
    end
    if self.url == nil || self.url == ""
      self.tries = 0
      log("ember boot ping: no url set")
      return
    end
    self.tries -= 1
    self.ticks = 20     # ~20 s before a retry if nothing answered
    http.post(self.url, "", / b, st -> self.done(st))
  end

  def done(status)
    if status > 0
      self.tries = 0
      log("ember boot ping: " + str(status))
    end
  end
end

return EmberBootPing()

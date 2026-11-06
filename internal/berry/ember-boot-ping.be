# @name    ember-boot-ping
# @desc    Tells the Ember server the clock rebooted, so it re-pushes its apps
# @author  ember
# @version 1.0
# @headless true
# @config  url text "Ember boot hook" default="__EMBER_BOOT_URL__" maxlen=96 help="Ember's POST /hooks/awtrix/boot URL"

class EmberBootPing
  var url
  var ticks
  var tries

  def init()
    self.url = store.get("url")
    self.ticks = 5
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
    self.ticks = 20
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

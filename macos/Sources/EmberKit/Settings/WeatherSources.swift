import Foundation

public struct WeatherSourceOption: Identifiable, Sendable {
    public let id: String
    public let name: String
    public let detail: LocalizedStringResource
    public var keyEnv: String? = nil
}

public enum WeatherSources {
    public static let openMeteo = "open-meteo"
    public static let defaultOpenMeteoModel = "best_match"
    public static let tomorrowIO = "tomorrow-io"

    public static let providers: [WeatherSourceOption] = [
        WeatherSourceOption(id: openMeteo, name: "Open-Meteo", detail: "Worldwide, free and without a key for non-commercial use, up to 10,000 calls a day. Rain comes in 15-minute steps. Air quality always comes from Open-Meteo. The model below decides what you get."),
        WeatherSourceOption(id: "met-no", name: "MET Norway", detail: "Worldwide, free and without a key. Finest in the Nordic countries; elsewhere it uses ECMWF's global model. Rain comes in hourly steps, and the chance of rain only in the Nordic area. It has no air quality, so that still comes from Open-Meteo."),
        WeatherSourceOption(id: tomorrowIO, name: "Tomorrow.io", detail: "Worldwide, with a free API key set on the server. The free plan allows 500 calls a day and 25 an hour, so Ember fetches at most every 5 minutes. Rain comes minute by minute for the next hour, then hourly; its days run from 6 am to 6 am. Air quality still comes from Open-Meteo.", keyEnv: "EMBER_TOMORROW_API_KEY"),
    ]

    public static let openMeteoModels: [WeatherSourceOption] = [
        WeatherSourceOption(id: defaultOpenMeteoModel, name: "Best match", detail: "Open-Meteo picks the best local model for your location and blends it with a global one. In much of central Europe that is DWD ICON, which can miss showers other models see. Has every value, UV included. Outside central Europe and the US, 15-minute rain is the hourly amount split evenly."),
        WeatherSourceOption(id: "ecmwf_ifs", name: "ECMWF IFS HRES (9 km)", detail: "The European Centre's high-resolution global model: worldwide, updated 4 times a day. No UV index; 15-minute rain is the hourly amount split evenly."),
        WeatherSourceOption(id: "ecmwf_ifs025", name: "ECMWF IFS (25 km)", detail: "ECMWF's coarser open-data run: worldwide, updated 4 times a day. Smooths out local showers. No UV index; 15-minute rain is the hourly amount split evenly."),
        WeatherSourceOption(id: "icon_seamless", name: "DWD ICON", detail: "Germany's models: 2 km over Germany and the Alps, 7 km over Europe, a coarser global run elsewhere. Updated every 3 hours near Germany. No UV index; outside the 2 km area, 15-minute rain is the hourly amount split evenly."),
        WeatherSourceOption(id: "gfs_seamless", name: "NOAA GFS / HRRR", detail: "The US models: 3 km HRRR over the contiguous US, updated hourly, and 13 to 25 km GFS elsewhere, where 15-minute rain is the hourly amount split evenly. Has every value, UV included."),
        WeatherSourceOption(id: "ukmo_seamless", name: "UK Met Office", detail: "2 km over the UK and Ireland, 10 km elsewhere. UV index only near the UK and only for the first two days; 15-minute rain is the hourly amount split evenly."),
    ]

    public static func provider(_ id: String) -> WeatherSourceOption? {
        providers.first { $0.id == id }
    }

    public static func isAvailable(_ id: String, in available: [String]?) -> Bool {
        guard provider(id)?.keyEnv != nil else { return true }
        return available?.contains(id) ?? false
    }

    public static func openMeteoModel(_ id: String) -> WeatherSourceOption? {
        openMeteoModels.first { $0.id == id }
    }
}

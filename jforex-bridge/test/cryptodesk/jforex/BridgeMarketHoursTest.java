package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.instrument.IMarketInfo;
import com.dukascopy.api.system.IClient;
import com.google.gson.*;
import java.lang.reflect.Field;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import static cryptodesk.jforex.BridgeTest.*;

/** Offline SDK metadata fixtures exercise the real instrument-catalog response. */
public final class BridgeMarketHoursTest {
    static void run() throws Exception {
        Field marketInfo = Instrument.class.getDeclaredField("marketInfo");
        marketInfo.setAccessible(true);
        Set<Instrument> offered = Set.of(Instrument.AAPLUSUSD, Instrument.EURUSD, Instrument.USDJPY, Instrument.BRENTCMDUSD);
        Map<Instrument, Object> originals = new HashMap<>();
        for (Instrument instrument : offered) originals.put(instrument, marketInfo.get(instrument));
        Path cache = Files.createTempDirectory("cryptodesk-jforex-market-hours-");
        AtomicBoolean connected = new AtomicBoolean();
        IContext context = fake(IContext.class, (method, values) -> null);
        IClient client = fake(IClient.class, (method, values) -> switch (method) {
            case "isConnected" -> connected.get();
            case "connect" -> { connected.set(true); yield null; }
            case "getAvailableInstruments" -> offered;
            case "startStrategy" -> { ((IStrategy) values[0]).onStart(context); yield 1L; }
            case "disconnect" -> { connected.set(false); yield null; }
            default -> null;
        });
        try {
            marketInfo.set(Instrument.AAPLUSUSD, fake(IMarketInfo.class, (method, values) -> switch (method) {
                case "getZoneId" -> ZoneId.of("America/Sao_Paulo");
                case "getFirstWorkingDay" -> DayOfWeek.WEDNESDAY;
                case "getLastWorkingDay" -> DayOfWeek.SUNDAY;
                case "getOpenTime" -> LocalTime.of(8, 12, 34, 987654321);
                case "getCloseTime" -> LocalTime.of(17, 45, 0, 123000000);
                case "isOpenNow", "isOpenAt", "isWeekendNow", "isWeekendAt", "untilOpen", "untilClose" ->
                    throw new AssertionError("catalog must not infer current instrument tradability");
                default -> null;
            }));
            marketInfo.set(Instrument.EURUSD, null);
            marketInfo.set(Instrument.USDJPY, fake(IMarketInfo.class, (method, values) -> switch (method) {
                case "getZoneId" -> ZoneId.of("UTC");
                case "getFirstWorkingDay" -> DayOfWeek.MONDAY;
                case "getLastWorkingDay" -> DayOfWeek.FRIDAY;
                case "getOpenTime" -> LocalTime.MIDNIGHT;
                default -> null; // Missing close time cannot become an invented session.
            }));
            marketInfo.set(Instrument.BRENTCMDUSD, fake(IMarketInfo.class, (method, values) -> {
                throw new IllegalStateException("explicit unavailable SDK metadata");
            }));
            try (Bridge bridge = new Bridge(() -> client, event -> {})) {
                JsonObject login = request("connect");
                login.addProperty("username", "offline-user"); login.addProperty("password", "offline-password");
                login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", cache.toString());
                good(bridge, login);
                JsonArray catalog = good(bridge, request("instruments")).getAsJsonArray("result");
                check(catalog.size() == offered.size(), "unavailable hours must not hide instruments");
                for (JsonElement value : catalog) {
                    JsonObject item = value.getAsJsonObject();
                    JsonObject metadata = item.getAsJsonObject("providerMetadata");
                    if (item.get("nativeId").getAsString().equals(Instrument.AAPLUSUSD.name())) {
                        check(metadata.get("marketHoursStatus").getAsString().equals("available"), "complete SDK session is available");
                        check(metadata.get("marketHoursSource").getAsString().equals("JForex regular market session"), "regular-session source is explicit");
                        check(metadata.get("marketHoursTimeZone").getAsString().equals("America/Sao_Paulo"), "market zone preserved instead of fixed UTC offset");
                        check(metadata.get("marketHoursFirstDay").getAsString().equals("3") && metadata.get("marketHoursLastDay").getAsString().equals("7"), "SDK weekdays use ISO numbers without assumed Mon-Fri");
                        check(metadata.get("marketHoursOpen").getAsString().equals("08:12:34") && metadata.get("marketHoursClose").getAsString().equals("17:45:00"), "session times always contain seconds and truncate fractional seconds");
                    } else {
                        check(metadata.get("marketHoursStatus").getAsString().equals("unavailable"), "missing, incomplete and throwing SDK records are explicit");
                        check(metadata.keySet().stream().noneMatch(key -> key.startsWith("marketHours") && !key.equals("marketHoursStatus")), "no partial or guessed hours escape into metadata");
                    }
                }
            }
        } finally {
            for (Instrument instrument : offered) marketInfo.set(instrument, originals.get(instrument));
            Files.delete(cache);
        }
    }
}

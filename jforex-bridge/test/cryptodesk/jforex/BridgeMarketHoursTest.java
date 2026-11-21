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
        delayedMarketResponse();
        instrumentCalendarFallback();
    }

    private static void delayedMarketResponse() throws Exception {
        Field marketInfo = Instrument.class.getDeclaredField("marketInfo"); marketInfo.setAccessible(true);
        Object original = marketInfo.get(Instrument.EURUSD);
        // Use the actual pinned SDK placeholder and its response-population
        // method: authentication precedes that asynchronous transport response.
        Class<?> type = Class.forName("com.dukascopy.api.instrument.MarketInfo");
        var constructor = type.getDeclaredConstructor(String.class); constructor.setAccessible(true);
        Object placeholder = constructor.newInstance("offline-delayed-market");
        var fill = type.getDeclaredMethod("addInfo", String.class, String.class, int.class, int.class); fill.setAccessible(true);
        Path cache = Files.createTempDirectory("cryptodesk-jforex-market-response-");
        try {
            marketInfo.set(Instrument.EURUSD, placeholder);
            AtomicBoolean connected = new AtomicBoolean();
            IContext context = fake(IContext.class, (method, values) -> null);
            IClient client = fake(IClient.class, (method, values) -> switch (method) {
                case "isConnected" -> connected.get();
                case "connect" -> { connected.set(true); yield null; }
                case "getAvailableInstruments" -> Set.of(Instrument.EURUSD);
                case "startStrategy" -> { ((IStrategy) values[0]).onStart(context); yield 1L; }
                case "disconnect" -> { connected.set(false); yield null; }
                default -> null;
            });
            try (Bridge bridge = new Bridge(() -> client, event -> {})) {
                JsonObject login = request("connect");
                login.addProperty("username", "offline-user"); login.addProperty("password", "offline-password");
                login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", cache.toString());
                good(bridge, login);
                check(((IMarketInfo) placeholder).getZoneId() == null, "actual SDK auth placeholder is incomplete");
                Thread response = new Thread(() -> {
                    try { Thread.sleep(100); fill.invoke(placeholder, "Offline delayed market", "UTC", 9 * 3600, 17 * 3600); }
                    catch (Exception error) { throw new AssertionError(error); }
                });
                response.start();
                JsonObject metadata = good(bridge, request("instruments")).getAsJsonArray("result").get(0).getAsJsonObject().getAsJsonObject("providerMetadata");
                response.join();
                check(metadata.get("marketHoursStatus").getAsString().equals("available"), "catalog waits for asynchronous SDK MarketResponse instead of persisting unavailable hours");
                check(metadata.get("marketHoursOpen").getAsString().equals("09:00:00"), "catalog contains response-populated actual SDK opening time");
            }
        } finally { marketInfo.set(Instrument.EURUSD, original); Files.delete(cache); }
    }

    private static void instrumentCalendarFallback() throws Exception {
        Field marketInfo = Instrument.class.getDeclaredField("marketInfo"); marketInfo.setAccessible(true);
        Set<Instrument> offered = Set.of(Instrument.EURUSD, Instrument.USDJPY, Instrument.AAPLUSUSD, Instrument.BRENTCMDUSD, Instrument.XAUUSD);
        Map<Instrument, Object> originals = new HashMap<>();
        for (Instrument instrument : offered) { originals.put(instrument, marketInfo.get(instrument)); marketInfo.set(instrument, null); }
        marketInfo.set(Instrument.EURUSD, fake(IMarketInfo.class, (method, values) -> switch (method) {
            case "getZoneId" -> ZoneId.of("UTC");
            case "getFirstWorkingDay" -> DayOfWeek.MONDAY;
            case "getLastWorkingDay" -> DayOfWeek.FRIDAY;
            case "getOpenTime", "getCloseTime" -> LocalTime.MIDNIGHT;
            default -> null;
        }));
        marketInfo.set(Instrument.AAPLUSUSD, fake(IMarketInfo.class, (method, values) -> switch (method) {
            case "getZoneId" -> ZoneId.of("UTC");
            case "getFirstWorkingDay" -> DayOfWeek.SUNDAY;
            case "getLastWorkingDay" -> DayOfWeek.FRIDAY;
            case "getOpenTime" -> LocalTime.of(22, 0);
            case "getCloseTime" -> LocalTime.of(21, 0);
            default -> null;
        }));
        long now = Instant.parse("2026-11-21T12:00:00Z").toEpochMilli(), day = 86_400_000L;
        long from = Instant.parse("2026-11-13T00:00:00Z").toEpochMilli(), to = from + 16 * day;
        long closed = Instant.parse("2026-11-20T22:00:00Z").toEpochMilli(), opened = closed + 2 * day;
        ITimeDomain first = domain(closed, closed + day), second = domain(closed + day - 1, opened);
        IDataService service = fake(IDataService.class, (method, values) -> {
            if (!method.equals("getOfflineTimeDomains")) throw new AssertionError("calendar extraction only reads instrument-specific offline domains");
            check(values.length == 3 && values[0].equals(from) && values[1].equals(to), "bounded UTC calendar horizon");
            if (values[2].equals(Instrument.EURUSD) || values[2].equals(Instrument.XAUUSD)) return new LinkedHashSet<>(List.of(second, first, domain(to + day, to + 2 * day)));
            if (values[2].equals(Instrument.USDJPY)) return Set.of(domain(closed, closed));
            if (values[2].equals(Instrument.BRENTCMDUSD)) throw new IllegalStateException("offline unavailable calendar");
            return Set.of();
        });
        AtomicBoolean connected = new AtomicBoolean();
        IContext context = fake(IContext.class, (method, values) -> method.equals("getDataService") ? service : null);
        IClient client = fake(IClient.class, (method, values) -> switch (method) {
            case "isConnected" -> connected.get();
            case "connect" -> { connected.set(true); yield null; }
            case "getAvailableInstruments" -> offered;
            case "startStrategy" -> { ((IStrategy) values[0]).onStart(context); yield 1L; }
            case "disconnect" -> { connected.set(false); yield null; }
            default -> null;
        });
        Path cache = Files.createTempDirectory("cryptodesk-jforex-calendar-hours-");
        try (Bridge bridge = new Bridge(() -> client, event -> {}, 45_000, () -> now)) {
            JsonObject login = request("connect");
            login.addProperty("username", "offline-user"); login.addProperty("password", "offline-password");
            login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", cache.toString());
            good(bridge, login);
            for (JsonElement element : good(bridge, request("instruments")).getAsJsonArray("result")) {
                JsonObject item = element.getAsJsonObject(), metadata = item.getAsJsonObject("providerMetadata");
                if (item.get("nativeId").getAsString().equals(Instrument.EURUSD.name()) || item.get("nativeId").getAsString().equals(Instrument.XAUUSD.name())) {
                    check(metadata.get("marketHoursSource").getAsString().equals("JForex instrument calendar"), "actual calendar source distinguished from regular MarketInfo");
                    check(metadata.get("marketHoursFrom").getAsLong() == from && metadata.get("marketHoursTo").getAsLong() == to, "calendar validity horizon retained");
                    JsonArray intervals = JsonParser.parseString(metadata.get("marketHoursClosedIntervals").getAsString()).getAsJsonArray();
                    check(intervals.size() == 1 && intervals.get(0).getAsJsonArray().get(0).getAsLong() == closed && intervals.get(0).getAsJsonArray().get(1).getAsLong() == opened, "calendar sorts/merges overlaps and preserves exact exclusive endpoints");
                    check(!metadata.has("marketHoursFirstDay"), "dated calendar never invents recurring regular hours");
                } else if (item.get("nativeId").getAsString().equals(Instrument.AAPLUSUSD.name())) {
                    check(metadata.get("marketHoursSource").getAsString().equals("JForex regular market session") && metadata.get("marketHoursFirstDay").getAsString().equals("7"), "unloaded calendar retains supplied ambiguous regular hours without inventing status");
                } else check(metadata.get("marketHoursStatus").getAsString().equals("unavailable"), "empty, malformed or failed calendar cannot prove continuous opening");
            }
        } finally {
            for (Instrument instrument : offered) marketInfo.set(instrument, originals.get(instrument));
            Files.delete(cache);
        }
    }

    private static ITimeDomain domain(long start, long end) {
        return fake(ITimeDomain.class, (method, values) -> switch (method) {
            case "getStart" -> start; case "getEnd" -> end; default -> null;
        });
    }
}

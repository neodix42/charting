package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.google.gson.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Download-only blocking server-loaded SDK collections, independent of chart callbacks. */
public final class BridgeNativeDownloadTest {
    static final long FROM = java.time.Instant.parse("2024-01-01T00:00:00Z").getEpochSecond();
    static final long TO = FROM + 3 * 86_400;
    static final class NativeLoad {
        final Object[] args;
        final CompletableFuture<List<IBar>> result = new CompletableFuture<>();
        final AtomicBoolean canceled = new AtomicBoolean();
        final AtomicBoolean returnAfterInterrupt = new AtomicBoolean();
        NativeLoad(Object[] args) { this.args = args; }
        void complete() { complete(Instrument.BTCUSD); }
        void complete(Instrument instrument) {
            check(args.length == 6 && args[0] == instrument && args[1] == Period.DAILY && args[2] == OfferSide.BID && args[3] == Filter.WEEKENDS
                && (Long) args[4] == FROM * 1000 && (Long) args[5] == (TO - 86_400) * 1000,
                "native getBars uses exact SDK native identity, BID, weekend filtering, first/last closed bar starts");
            result.complete(List.of(bar(FROM * 1000, 1, 3, .5, 2), bar((FROM + 86_400) * 1000, 2, 4, 1, 3), bar((TO - 86_400) * 1000, 3, 5, 2, 4)));
        }
    }
    static final class NativeFixture implements AutoCloseable {
        final BlockingQueue<NativeLoad> calls = new LinkedBlockingQueue<>();
        final Fixture charts;
        final Bridge bridge;
        NativeFixture(long chartTimeout, long downloadTimeout) throws Exception {
            this(chartTimeout, downloadTimeout, null);
        }
        NativeFixture(long chartTimeout, long downloadTimeout, IDataService calendar) throws Exception {
            this(chartTimeout,downloadTimeout,calendar,(TO+3_600)*1000);
        }
        NativeFixture(long chartTimeout, long downloadTimeout, IDataService calendar, long now) throws Exception {
            charts = new Fixture(chartTimeout, () -> now, 2_000, downloadTimeout, Set.of(Instrument.BTCUSD, Instrument.EURUSD, Instrument.XAUUSD, Instrument.AAPLUSUSD, Instrument.USA500IDXUSD), (method, args) -> {
                NativeLoad call = new NativeLoad(args); calls.add(call);
                try { return call.result.get(); }
                catch (InterruptedException error) {
                    call.canceled.set(true);
                    if (call.returnAfterInterrupt.get()) return List.of(bar((Long) args[4], 1, 3, .5, 2));
                    throw error;
                }
            }, calendar);
            bridge = charts.bridge;
        }
        NativeLoad load() throws Exception { NativeLoad load = calls.poll(2, TimeUnit.SECONDS); check(load != null, "blocking SDK getBars requested only for native download"); return load; }
        public void close() throws Exception { charts.close(); }
    }
    static JsonObject history(boolean download) {
        return history(Instrument.BTCUSD, "cfd", download);
    }
    static JsonObject history(Instrument instrument, String market, boolean download) {
        JsonObject request = request("history"), key = feed(instrument, market); key.addProperty("timeframe", "1d");
        request.add("feedKey", key); request.addProperty("from", FROM); request.addProperty("to", TO); request.addProperty("limit", 1000);
        if (download) request.addProperty("download", true); return request;
    }
    static CompletableFuture<JsonObject> dispatch(NativeFixture f, JsonObject request) {
        CompletableFuture<JsonObject> reply = new CompletableFuture<>(); f.bridge.dispatch(request, reply::complete); return reply;
    }
    static void failed(JsonObject response, String message) { check(!response.get("ok").getAsBoolean(), message + ": " + response); }
    static IDataService calendar(boolean loaded, boolean read, long validFrom, long validTo, List<org.threeten.extra.Interval> closed) {
        com.dukascopy.holiday.RegularWeeklyHolidayScheduleDetail rule = fake(com.dukascopy.holiday.RegularWeeklyHolidayScheduleDetail.class,
            (method, args) -> method.equals("getDetailInterval") ? org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(validFrom), java.time.Instant.ofEpochMilli(validTo)) : null);
        com.dukascopy.holiday.HolidaySchedule schedule = fake(com.dukascopy.holiday.HolidaySchedule.class, (method, args) ->
            method.equals("getRegularWeeklyHolidayScheduleDetails") && validFrom < validTo ? List.of(rule) : List.of());
        return calendarWithSchedule(loaded, read ? schedule : null, closed);
    }
    static IDataService calendarWithSchedule(boolean loaded, com.dukascopy.holiday.HolidaySchedule schedule, List<org.threeten.extra.Interval> closed) {
        return calendarWithScheduleSupplier(loaded, () -> schedule, closed);
    }
    static IDataService calendarWithScheduleSupplier(boolean loaded, java.util.function.Supplier<com.dukascopy.holiday.HolidaySchedule> schedule, List<org.threeten.extra.Interval> closed) {
        return calendarWithMetadata(loaded, schedule, closed, null);
    }
    static IDataService calendarWithMetadata(boolean loaded, java.util.function.Supplier<com.dukascopy.holiday.HolidaySchedule> schedule, List<org.threeten.extra.Interval> closed, com.dukascopy.charts.data.datacache.metadata.IFeedMetadataManager metadata) {
        return calendarWithMetadata(loaded, schedule, closed, metadata, null);
    }
    static IDataService calendarWithMetadata(boolean loaded, java.util.function.Supplier<com.dukascopy.holiday.HolidaySchedule> schedule, List<org.threeten.extra.Interval> closed, com.dukascopy.charts.data.datacache.metadata.IFeedMetadataManager metadata, com.dukascopy.charts.data.datacache.ICurvesProtocolHandler handler) {
        var splits = fake(com.dukascopy.charts.data.datacache.splits.ISplitsManager.class,(method,args) -> null);
        var commissions = fake(com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager.class,(method,args) -> method.equals("applyFeedCommissionToData") ? args[3] : null);
        return calendarWithMetadata(loaded,schedule,closed,metadata,handler,splits,commissions);
    }
    static IDataService calendarWithMetadata(boolean loaded, java.util.function.Supplier<com.dukascopy.holiday.HolidaySchedule> schedule, List<org.threeten.extra.Interval> closed, com.dukascopy.charts.data.datacache.metadata.IFeedMetadataManager metadata, com.dukascopy.charts.data.datacache.ICurvesProtocolHandler handler, com.dukascopy.charts.data.datacache.splits.ISplitsManager splits, com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager commissions) {
        var holidays = fake(com.dukascopy.dds2.greed.holiday.ClientHolidayScheduleService.class, (method, args) -> switch(method) {
            case "hasHolidaysScheduleLoaded" -> loaded;
            case "getHolidaySchedule" -> schedule.get();
            case "getHolidayIntervals" -> closed != null ? closed : schedule.get().getHolidayIntervalsSortedAndMerged((Long) args[1], (Long) args[2]);
            default -> null;
        });
        var provider = fake(com.dukascopy.charts.data.datacache.IFeedDataProvider.class, (method, args) -> switch(method) {
            case "getHolidayScheduleService" -> holidays;
            case "getFeedMetadataManager" -> metadata;
            case "getICurvesProtocolHandler" -> handler;
            case "getSplitsManager" -> splits;
            case "getFeedCommissionManager" -> commissions;
            case "getTimeOfFirstCandle" -> metadata == null ? Long.MAX_VALUE : metadata.getTimeOfFirstCandle((Instrument) args[0], (Period) args[1]);
            default -> null;
        });
        return new com.dukascopy.api.impl.connect.JForexDataService(provider, new Properties());
    }
    public static void main(String[] args) throws Exception {
        // Exercise the pinned provider's real custom-only READ semantics, not
        // a mock assumption about whether listed dates mean open or closed.
        var custom = new com.dukascopy.holiday.CustomHolidayScheduleDetailImpl(1, java.time.ZoneOffset.UTC,
            java.time.LocalDateTime.parse("2025-12-25T00:00:00"), java.time.LocalDateTime.parse("2025-12-25T23:59:00"));
        var customOnly = new com.dukascopy.holiday.HolidayScheduleImpl.Builder(1).addCustomHolidayScheduleDetail(custom).build();
        check(customOnly.isHoliday(java.time.Instant.parse("2025-12-25T12:00:00Z").toEpochMilli()), "custom READ dates are actual provider closure intervals");
        check(!customOnly.isHoliday(FROM * 1000) && customOnly.getHolidayIntervalsSortedAndMerged(FROM * 1000, TO * 1000).isEmpty(),
            "loaded custom-only READ schedule explicitly reports other historical dates open, without reversing open/closed exceptions");
        check(customOnly.getRegularHourlyHolidayScheduleDetails().isEmpty() && customOnly.getRegularDailyHolidayScheduleDetails().isEmpty()
            && customOnly.getRegularWeeklyHolidayScheduleDetails().isEmpty() && customOnly.getRegularMonthlyHolidayScheduleDetails().isEmpty()
            && customOnly.getRegularYearlyHolidayScheduleDetails().isEmpty(), "custom-only provider proof has no recurring rules");
        check(Bridge.verifiedHistoricalClosures(calendarWithSchedule(true, customOnly, null), Instrument.BTCUSD, FROM * 1000, TO * 1000).isEmpty(),
            "a loaded custom-only READ schedule verifies other historical dates, without assuming an unloaded public empty set means24x7");
        var diagnostic = Bridge.historicalCalendarDiagnostic(calendarWithSchedule(true, customOnly, null), Instrument.BTCUSD, FROM * 1000, TO * 1000);
        check(Boolean.TRUE.equals(diagnostic.get("loaded")) && Boolean.TRUE.equals(diagnostic.get("readSchedule")) && ((List<?>) diagnostic.get("rules")).size() ==1
            && diagnostic.get("closedIntervalCount").equals(0), "bounded calendar diagnostic includes only the actual provider schedule and historical interval result");
        try (NativeFixture f = new NativeFixture(75, 2_000)) {
            CompletableFuture<JsonObject> nativeReply = dispatch(f, history(true)); NativeLoad nativeLoad = f.load();
            CompletableFuture<JsonObject> chartReply = dispatch(f, history(false)); Load chartLoad = f.charts.load();
            JsonObject chart = chartReply.get(2, TimeUnit.SECONDS); failed(chart, "ordinary chart history retains its short deadline");
            check(chart.getAsJsonObject("error").get("message").getAsString().contains("historical data loading timed out") && !nativeReply.isDone(),
                "blocking server-loaded collection survives chart deadline without relying on background cache callbacks");
            await(() -> chartLoad.progress().stopJob(), "chart deadline still cancels its own async SDK loader");
            nativeLoad.complete(); JsonObject result = nativeReply.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() == 3
                && result.getAsJsonArray("result").get(2).getAsJsonObject().get("close").getAsDouble() == 4,
                "slow native daily load publishes the exact server-loaded closed OHLC collection");
            check(result.getAsJsonArray("result").asList().stream().allMatch(value -> value.getAsJsonObject().get("closed").getAsBoolean()), "download cannot publish unfinished candles");
            await(f.charts.subscribed::isEmpty, "chart and download release separate subscription references");
            CompletableFuture<JsonObject> nextChart = dispatch(f, history(false)); f.charts.load();
            JsonObject chartAgain = nextChart.get(2, TimeUnit.SECONDS); failed(chartAgain, "download API and budget never leak to a reused chart worker");
            check(f.calls.isEmpty() && !chartAgain.getAsJsonObject("error").get("message").getAsString().contains("native daily download"), "chart remains on its independent async API");
        }
        // Model the observed cold-cache symptom: async background completion
        // reports no callback rows while the authoritative loaded collection is
        // still pending. The download must await getBars rather than publish [].
        try (NativeFixture f = new NativeFixture(1_000, 2_000)) {
            CompletableFuture<JsonObject> download = dispatch(f, history(true)); NativeLoad server = f.load();
            CompletableFuture<JsonObject> chart = dispatch(f, history(false)); f.charts.load().finish(true);
            check(chart.get(2, TimeUnit.SECONDS).getAsJsonArray("result").isEmpty() && !download.isDone(), "empty background callbacks do not certify a pending server-loaded download");
            server.complete(); check(download.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 3 && f.charts.loads.isEmpty(), "native download uses only its actual getBars collection");
        }
        for (Instrument instrument : List.of(Instrument.EURUSD, Instrument.XAUUSD, Instrument.AAPLUSUSD)) {
            try (NativeFixture f = new NativeFixture(50, 1_000)) {
                CompletableFuture<JsonObject> download = dispatch(f, history(instrument, Bridge.market(instrument), true));
                f.load().complete(instrument); JsonObject result = download.get(2, TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() == 3 && f.charts.loads.isEmpty(),
                    "ordinary forex, metal and stock identities use the same bounded server-loaded daily SDK API");
                await(f.charts.subscribed::isEmpty, "ordinary download releases its subscription reference");
            }
        }
        long hour = 3_600_000, fromMs = FROM * 1000;
        for (String kind : List.of("holiday", "verified-empty", "unloaded", "no-read", "expired", "native", "custom-only", "custom-closure")) {
            boolean nativeHours = kind.equals("native"), loaded = !kind.equals("unloaded"), read = !kind.equals("no-read") && !nativeHours;
            long validFrom = kind.equals("expired") ? fromMs + 365 * 86_400_000L : 0;
            long validTo = kind.equals("verified-empty") ? validFrom : fromMs + 1000 * 86_400_000L;
            var closed = kind.equals("holiday") ? List.of(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(fromMs + hour + 1_800_000), java.time.Instant.ofEpochMilli(fromMs + hour + 2_700_000))) : List.<org.threeten.extra.Interval>of();
            var partialClosure = new com.dukascopy.holiday.CustomHolidayScheduleDetailImpl(2, java.time.ZoneOffset.UTC,
                java.time.LocalDateTime.parse("2024-01-01T01:30:00"), java.time.LocalDateTime.parse("2024-01-01T01:44:00"));
            var partialSchedule = new com.dukascopy.holiday.HolidayScheduleImpl.Builder(2).addCustomHolidayScheduleDetail(partialClosure).build();
            IDataService service = kind.equals("custom-only") ? calendarWithSchedule(true, customOnly, null) : kind.equals("custom-closure") ? calendarWithSchedule(true, partialSchedule, null) : calendar(loaded, read, validFrom, validTo, closed);
            try (NativeFixture f = new NativeFixture(50, 1_000, service)) {
                JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("to", FROM + 3 * 3600); query.addProperty("fullOpenHours", !nativeHours);
                CompletableFuture<JsonObject> response = dispatch(f, query);
                if (List.of("unloaded", "no-read", "expired").contains(kind)) {
                    if (kind.equals("unloaded") || kind.equals("no-read")) f.load().result.complete(List.of(bar(fromMs, 1, 3, .5, 2)));
                    failed(response.get(2, TimeUnit.SECONDS), "unverified historical calendar fails before quote reads " + kind);
                    check(f.calls.isEmpty(), "unknown calendar cannot request/certify source data");
                    await(f.charts.subscribed::isEmpty, "failed calendar source releases subscription"); continue;
                }
                NativeLoad request = f.load();
                check(request.args[1] == Period.ONE_HOUR && request.args[2] == OfferSide.BID && request.args[3] == Filter.NO_FILTER && (Long) request.args[4] == fromMs && (Long) request.args[5] == fromMs + 2 * hour,
                    "hourly source uses actual bounded native BID buckets");
                request.result.complete(List.of(bar(fromMs, 1, 3, .5, 2), bar(fromMs + hour, 2, 4, 1, 3), bar(fromMs + 2 * hour, 3, 5, 2, 4)));
                JsonObject result = response.get(2, TimeUnit.SECONDS);
                if (List.of("unloaded", "no-read", "expired").contains(kind)) failed(result, "unverified historical calendar fails closed " + kind);
                else {
                    check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() == (kind.equals("holiday") || kind.equals("custom-closure") ? 2 : 3), "only verified full hours pass, native-only aggregation preserves all provider hours " + kind);
                    if (kind.equals("holiday") || kind.equals("custom-closure")) check(result.getAsJsonArray("result").get(1).getAsJsonObject().get("time").getAsLong() == FROM + 2 * 3600, "a closure inside one hour removes the complete intersecting source hour");
                }
                await(f.charts.subscribed::isEmpty, "hourly source failure or success releases subscription");
            }
        }
        var lateRead = new AtomicReference<com.dukascopy.holiday.HolidaySchedule>();
        try (NativeFixture f = new NativeFixture(50, 1_000, calendarWithScheduleSupplier(true, lateRead::get, null))) {
            JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("to", FROM + 3 * 3600); query.addProperty("fullOpenHours", true);
            CompletableFuture<JsonObject> response = dispatch(f, query); NativeLoad source = f.load();
            lateRead.set(customOnly); source.result.complete(List.of(bar(fromMs, 1, 3, .5, 2)));
            JsonObject result = response.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() ==1,
                "loaded flag published before READ map gets one bounded day warm-up, but source rows require the actual READ proof");
        }
        try (NativeFixture f = new NativeFixture(50, 2_000, calendar(true, true, 0, fromMs + 1000 * 86_400_000L, List.of()))) {
            JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("fullOpenHours", true);
            CompletableFuture<JsonObject> response = dispatch(f, query);
            for (int index =0; index <3; index++) {
                NativeLoad source = f.load(); long start = fromMs + index * 86_400_000L;
                check(source.args[3] == Filter.NO_FILTER && (Long) source.args[4] == start && (Long) source.args[5] == start + 23 * hour,
                    "large exports split into exact UTC source days inside one bounded job");
                IBar ambiguous = new com.dukascopy.charts.data.datacache.CandleData(start + hour, 2, 2, 2, 2, 0);
                IBar zeroVolumeObservation = new com.dukascopy.charts.data.datacache.CandleData(start + 23 * hour, 2, 3, 1, 4, 0);
                source.result.complete(List.of(bar(start, 1, 3, .5, 2), ambiguous, zeroVolumeObservation));
            }
            JsonObject result = response.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() ==6,
                "daily SDK reads preserve genuine changing zero-volume observations but never manufacture zero-volume flat gap carry");
        }
        for (String kind : List.of("closed", "empty-open", "all-filled")) {
            var closed = kind.equals("closed") ? List.of(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(fromMs), java.time.Instant.ofEpochMilli(fromMs + 3 * hour -1))) : List.<org.threeten.extra.Interval>of();
            try (NativeFixture f = new NativeFixture(50, 1_000, calendar(true, true, 0, fromMs + 1000 * 86_400_000L, closed))) {
                JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("to", FROM + 3 * 3600); query.addProperty("fullOpenHours", true);
                CompletableFuture<JsonObject> response = dispatch(f, query);
                if (kind.equals("closed")) {
                    JsonObject result = response.get(2, TimeUnit.SECONDS);
                    check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").isEmpty() && f.calls.isEmpty(), "fully closed calendar range is valid empty coverage without a quote request");
                } else {
                    f.load().result.complete(kind.equals("empty-open") ? List.of() : List.of(new com.dukascopy.charts.data.datacache.CandleData(fromMs, 2, 2, 2, 2, 0)));
                    JsonObject result = response.get(2, TimeUnit.SECONDS); failed(result, "open empty/filler source cannot establish verified coverage");
                    check(result.getAsJsonObject("error").get("message").getAsString().contains("incomplete history was not cached"), "false empty cannot poison a source manifest");
                }
            }
        }
        var fullClosedDay = new com.dukascopy.holiday.CustomHolidayScheduleDetailImpl(3, java.time.ZoneOffset.UTC,
            java.time.LocalDateTime.parse("2024-01-02T00:00:00"), java.time.LocalDateTime.parse("2024-01-02T23:59:00"));
        var nativeHoliday = new com.dukascopy.holiday.HolidayScheduleImpl.Builder(3).addCustomHolidayScheduleDetail(fullClosedDay).build();
        try (NativeFixture f = new NativeFixture(50, 2_000, calendarWithSchedule(true, nativeHoliday, null))) {
            JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("fullOpenHours", false);
            CompletableFuture<JsonObject> response = dispatch(f, query);
            f.load().result.complete(List.of(bar(fromMs, 1, 3, .5, 2)));
            f.load().result.complete(List.of());
            f.load().result.complete(List.of(bar(fromMs + 2 * 86_400_000L, 2, 4, 1, 3)));
            JsonObject result = response.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() ==2,
                "native real/fully closed/real UTC days preserve both real source days without inventing a holiday candle");
        }
        for (String kind : List.of("all-closed", "open", "partial", "unverified")) {
            var closures = new ArrayList<org.threeten.extra.Interval>();
            if (kind.equals("all-closed")) {
                // Adjacent and overlapping intervals must form a complete
                // union; requiring one interval alone would reject real gaps.
                closures.add(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(fromMs + hour), java.time.Instant.ofEpochMilli(fromMs + 3 * hour -1)));
                closures.add(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(fromMs), java.time.Instant.ofEpochMilli(fromMs + hour -1)));
            } else if (kind.equals("partial")) for (int index =0; index<3; index++)
                closures.add(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(fromMs + index * hour), java.time.Instant.ofEpochMilli(fromMs + index * hour + hour /2)));
            try (NativeFixture f = new NativeFixture(50, 1_000, calendar(true, !kind.equals("unverified"), 0, fromMs + 1000 * 86_400_000L, closures))) {
                JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h"); query.addProperty("to", FROM + 3 * 3600); query.addProperty("fullOpenHours", false);
                CompletableFuture<JsonObject> response = dispatch(f, query); f.load().result.complete(List.of());
                JsonObject result = response.get(2, TimeUnit.SECONDS);
                if (kind.equals("all-closed")) check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").isEmpty(), "native fully closed union is a verified source gap, never a fabricated candle");
                else failed(result, "native zero requires full continuous closure proof, not absence of a fully open hour " + kind);
            }
        }
        for (String invalid : List.of("instrument", "market", "timeframe", "open", "alignment", "range", "limit", "flag")) {
            try (NativeFixture f = new NativeFixture(50, 1_000)) {
                JsonObject request = history(true), key = request.getAsJsonObject("feedKey");
                switch (invalid) {
                    case "instrument" -> key.addProperty("instrument", "BTCXAU");
                    case "market" -> key.addProperty("market", "metal");
                    case "timeframe" -> key.addProperty("timeframe", "1w");
                    case "open" -> request.addProperty("to", TO + 86_400);
                    case "alignment" -> request.addProperty("from", FROM + 1);
                    case "range" -> request.addProperty("from", TO - 367 * 86_400);
                    case "limit" -> request.addProperty("limit", 1001);
                    case "flag" -> request.addProperty("download", "true");
                }
                failed(dispatch(f, request).get(1, TimeUnit.SECONDS), "invalid download scope " + invalid);
                check(f.calls.isEmpty() && f.charts.loads.isEmpty(), "invalid longer-deadline request never reaches either SDK API");
            }
        }
        for (String kind : List.of("empty", "null", "oversized", "price", "unaligned", "outside", "duplicate", "order", "failure")) {
            try (NativeFixture f = new NativeFixture(50, 1_000)) {
                CompletableFuture<JsonObject> download = dispatch(f, history(true)); NativeLoad load = f.load();
                switch (kind) {
                    case "empty" -> load.result.complete(List.of());
                    case "null" -> load.result.complete(null);
                    case "oversized" -> load.result.complete(Collections.nCopies(367, bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "price" -> load.result.complete(List.of(bar(FROM * 1000, -1, 2, .5, 1.5)));
                    case "unaligned" -> load.result.complete(List.of(bar(FROM * 1000 + 1, 1, 2, .5, 1.5)));
                    case "outside" -> load.result.complete(List.of(bar(TO * 1000, 1, 2, .5, 1.5)));
                    case "duplicate" -> load.result.complete(List.of(bar(FROM * 1000, 1, 2, .5, 1.5), bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "order" -> load.result.complete(List.of(bar((FROM + 86_400) * 1000, 1, 2, .5, 1.5), bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "failure" -> load.result.completeExceptionally(new JFException("explicit offline source failure"));
                }
                JsonObject result = download.get(2, TimeUnit.SECONDS);
                if (kind.equals("empty")) check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").isEmpty(), "genuine loaded server-empty range remains empty with no fabricated OHLC");
                else failed(result, "invalid loaded collection cannot publish " + kind);
                await(f.charts.subscribed::isEmpty, "collection success/failure releases native subscription");
            }
        }
        try (NativeFixture f = new NativeFixture(50, 200)) {
            CompletableFuture<JsonObject> stalled = dispatch(f, history(true)); NativeLoad first = f.load();
            JsonObject failure = stalled.get(2, TimeUnit.SECONDS); failed(failure, "blocking native download remains bounded");
            check(failure.getAsJsonObject("error").get("kind").getAsString().equals("timeout")
                && failure.getAsJsonObject("error").get("message").getAsString().equals("JForex native daily download timed out. Completed history is retained; retry to resume."), "native deadline has exact retryable scope");
            await(first.canceled::get, "native deadline interrupts its bounded SDK collection worker");
            CompletableFuture<JsonObject> resumed = dispatch(f, history(true)); f.load().complete();
            check(resumed.get(2, TimeUnit.SECONDS).get("ok").getAsBoolean() && f.charts.connected.get(), "explicit retry reuses healthy SDK session");
        }
        try (NativeFixture f = new NativeFixture(50, 250)) {
            CompletableFuture<JsonObject> first = dispatch(f, history(true)); f.load();
            CompletableFuture<JsonObject> second = dispatch(f, history(true)); f.load();
            CompletableFuture<JsonObject> queued = dispatch(f, history(true));
            failed(first.get(2, TimeUnit.SECONDS), "first native worker remains bounded"); failed(second.get(2, TimeUnit.SECONDS), "second native worker remains bounded");
            NativeLoad queuedLoad = f.load();
            CompletableFuture<JsonObject> clock = dispatch(f, history(false)); f.charts.load(); failed(clock.get(2, TimeUnit.SECONDS), "short chart deadline advances after queued work starts");
            check(!queued.isDone(), "queued native work receives its full SDK budget at executor entry");
            queuedLoad.complete(); check(queued.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 3, "queued native collection succeeds exactly once");
        }
        BridgeHourlyRecoveryTest.run();
        BridgeYearlyRecoveryTest.run();
        System.out.println("JForex native loaded getBars collections, scoped download deadlines, genuine empty ranges, validation, queueing and chart isolation checks passed");
    }
}

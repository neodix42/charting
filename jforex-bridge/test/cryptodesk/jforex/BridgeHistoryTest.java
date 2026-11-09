package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.google.gson.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Offline production-pipeline checks for candle-first unfinished D1/W1 data. */
public final class BridgeHistoryTest {
    static final long NOW = Instant.parse("2026-10-28T12:34:00Z").toEpochMilli();
    static long current(String interval) {
        return Instant.parse(interval.equals("1w") ? "2026-10-26T00:00:00Z" : "2026-10-28T00:00:00Z").toEpochMilli();
    }
    static List<JsonObject> barEvents(Fixture f) { return f.events.stream().filter(event -> event.has("bars")).toList(); }
    static boolean state(Fixture f, String expected) {
        return f.events.stream().anyMatch(event -> event.has("health") && event.getAsJsonObject("health").get("state").getAsString().equals(expected));
    }
    static JsonObject history(Fixture f, String interval, long from, long to, int limit) {
        JsonObject query = f.history("historical-window");
        query.getAsJsonObject("feedKey").addProperty("timeframe", interval);
        query.addProperty("from", from / 1000); query.addProperty("to", to / 1000); query.addProperty("limit", limit);
        return query;
    }
    static record Window(Period period, long from, long end) { }
    static List<Window> windows(String interval, long current, long now) {
        long cutoff = Math.min(now, current + Bridge.PERIODS.get(interval).getInterval() - 1);
        long day = Math.floorDiv(cutoff, Period.DAILY.getInterval()) * Period.DAILY.getInterval();
        long hour = Math.floorDiv(cutoff, Period.ONE_HOUR.getInterval()) * Period.ONE_HOUR.getInterval();
        long minute = Math.floorDiv(cutoff, Period.ONE_MIN.getInterval()) * Period.ONE_MIN.getInterval();
        List<Window> result = new ArrayList<>();
        if (interval.equals("1w") && current < day) result.add(new Window(Period.DAILY, current, day - Period.DAILY.getInterval()));
        if (day < hour) result.add(new Window(Period.ONE_HOUR, day, hour - Period.ONE_HOUR.getInterval()));
        result.add(new Window(Period.ONE_MIN, hour, minute));
        return result;
    }
    static void assertWindow(Load load, Window window) {
        check(load.range() && load.args[1].equals(window.period()) && load.from() == window.from() && load.end() == window.end(),
            "unfinished long candles request only their bounded native basic-bar window: expected " + window + " got " + Arrays.toString(load.args));
        long count = (load.end() - load.from()) / window.period().getInterval() + 1;
        check(count <= (window.period().equals(Period.DAILY) ? 6 : window.period().equals(Period.ONE_HOUR) ? 23 : 60),
            "current context never requests full unrelated intraday history");
        check(load.args[2] == OfferSide.BID && load.args[3] == Filter.WEEKENDS, "context sources preserve native Bid/weekend semantics");
    }
    static void emit(Load load, long time, double open, double high, double low, double close, double volume) {
        ((LoadingDataListener) load.args[load.args.length - 2]).newBar((Instrument) load.args[0], (Period) load.args[1],
            OfferSide.BID, time, open, close, low, high, volume);
    }
    static void finishSnapshot(Fixture f, String interval, long current, long now, Load first, boolean empty) throws Exception {
        List<Window> windows = windows(interval, current, now);
        for (int i = 0; i < windows.size(); i++) {
            Load load = i == 0 && first != null ? first : f.load();
            assertWindow(load, windows.get(i));
            if (empty) load.finish(true); else load.complete(true);
        }
    }
    static JsonObject lastBar(Fixture f) {
        List<JsonObject> events = barEvents(f);
        return events.get(events.size() - 1).getAsJsonArray("bars").get(0).getAsJsonObject();
    }

    public static void main(String[] args) throws Exception {
        for (String interval : List.of("1d", "1w")) {
            Period period = Bridge.PERIODS.get(interval); long step = period.getInterval(), current = current(interval);
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                good(f.bridge, f.subscription("cold-long-period", interval)); Load closed = f.load();
                check(!closed.range() && closed.args[1] == period && (Integer) closed.args[4] == 499 && closed.end() == current - step,
                    "cold long history preserves native closed target bars and excludes the unfinished candle");
                closed.emit(current - 2 * step); closed.complete(true); Load active = f.load();
                List<JsonObject> published = barEvents(f);
                check(published.size() == 1 && published.get(0).getAsJsonArray("bars").size() == 2,
                    "closed target candles publish before a stalled current context load");
                JsonObject checkpoint = published.get(0);
                check(checkpoint.get("coverageFrom").getAsLong() == (current - 2 * step) / 1000
                    && checkpoint.get("coverageTo").getAsLong() == current / 1000,
                    "closed checkpoint excludes the unfinished context");
                check(checkpoint.getAsJsonArray("bars").asList().stream().allMatch(value -> value.getAsJsonObject().get("closed").getAsBoolean()),
                    "native historical candles remain authoritative");
                f.strategy.get().onTick(Instrument.EURUSD, tick(NOW + 1_000, 1.22));
                f.strategy.get().onTick(Instrument.EURUSD, tick(NOW, 1.01));
                check(!state(f, "live") && state(f, "reconciling"), "out-of-order quotes cannot hide pending context");
                finishSnapshot(f, interval, current, NOW, active, false);
                await(() -> state(f, "live"), "basic candle context completes reconciliation");
                JsonObject last = lastBar(f);
                check(last.get("time").getAsLong() == current / 1000 && last.get("open").getAsDouble() == 1.1
                    && last.get("close").getAsDouble() == 1.22 && last.get("high").getAsDouble() == 1.22
                    && last.get("low").getAsDouble() == 1.09 && last.get("volume").getAsDouble() == 2.5 * windows(interval, current, NOW).size(),
                    "hierarchical context sums each source once and preserves newer real ticks without rewinding opening context");
                check(!last.get("closed").getAsBoolean() && last.get("quality").getAsString().equals("provisional"),
                    "derived unfinished long candle remains explicitly provisional");
                check(last.get("sourceRevision").getAsLong() > checkpoint.getAsJsonArray("bars").get(0).getAsJsonObject().get("sourceRevision").getAsLong(),
                    "merged current quote has a newer revision than closed history");
                check(f.blockingCacheReads.get() == 0 && f.loads.isEmpty(), "no synchronous or unrelated history/tick requests occur");
            }
            try (Fixture f = new Fixture(5_000, () -> NOW, 100)) {
                JsonObject request = f.subscription("warm-long-period", interval); request.addProperty("historyFrom", (current - 2 * step) / 1000);
                good(f.bridge, request); Load closed = f.load();
                check(closed.range() && closed.args[1] == period && closed.from() == current - 2 * step && closed.end() == current - step,
                    "warm chart fetches only native closed missing overlap");
                closed.complete(true); Load active = f.load(); assertWindow(active, windows(interval, current, NOW).get(0)); active.finish(false);
                await(() -> retrying(f), "failed current component remains visible and cannot publish a partial target");
                check(!state(f, "live") && barEvents(f).size() == 1, "failed current component preserves completed history");
                Load retry = f.load(); check(retry.args[1] == period && retry.from() == current - step && retry.end() == current - step,
                    "retry reuses the already durable target checkpoint");
                retry.finish(true); Load canceled = f.load(); f.unsubscribe("warm-long-period");
                await(() -> canceled.progress().stopJob(), "detach cancels current basic-candle loading");
                int before = f.events.size(); canceled.complete(true); check(f.events.size() == before, "late canceled context never publishes");
            }
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current - 3 * step, current, 500), reply::complete); Load closed = f.load();
                check(closed.args[1] == period && closed.range() && closed.from() == current - 3 * step && closed.end() == current - step,
                    "narrow historical target gap loads only its requested native range");
                closed.emit(current - 3 * step); closed.emit(current - step); closed.finish(true);
                JsonArray result = reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(result.size() == 2 && result.get(0).getAsJsonObject().get("time").getAsLong() == (current - 3 * step) / 1000
                    && f.loads.isEmpty(), "past-only pagination preserves actual gaps and skips every current context request");
                CompletableFuture<JsonObject> recentReply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current - 2 * step, NOW + 1000, 2), recentReply::complete); Load recent = f.load();
                check(recent.args[1] == period && recent.from() == current - 2 * step && recent.end() == current - step,
                    "recent history loads native closed bars independently");
                recent.emit(current - 2 * step); recent.complete(true); Load first = f.load();
                check(!recentReply.isDone(), "RPC awaits its current context while streaming seeds publish closed bars separately");
                finishSnapshot(f, interval, current, NOW, first, false);
                JsonArray latest = recentReply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(latest.size() == 2 && latest.get(0).getAsJsonObject().get("time").getAsLong() == (current - step) / 1000
                    && latest.get(0).getAsJsonObject().get("closed").getAsBoolean()
                    && latest.get(1).getAsJsonObject().get("time").getAsLong() == current / 1000
                    && !latest.get(1).getAsJsonObject().get("closed").getAsBoolean(), "RPC keeps newest limit and native/provisional boundary");
                CompletableFuture<JsonObject> currentReply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current, NOW + 1000, 1), currentReply::complete);
                finishSnapshot(f, interval, current, NOW, null, false);
                check(currentReply.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 1 && f.loads.isEmpty(),
                    "current-only RPC skips full target bootstrap and unrelated histories");
            }
            AtomicLong rollover = new AtomicLong(current + step - 1_000);
            try (Fixture f = new Fixture(5_000, rollover::get)) {
                good(f.bridge, f.subscription("rollover", interval)); f.load().complete(true);
                List<Window> windows = windows(interval, current, rollover.get());
                for (int i = 0; i < windows.size() - 1; i++) { Load load = f.load(); assertWindow(load, windows.get(i)); load.complete(true); }
                Load minute = f.load(); assertWindow(minute, windows.get(windows.size() - 1));
                emit(minute, minute.end(), 1.01, 1.04, .99, 1.03, 1.5);
                rollover.set(current + step + 1_000); minute.finish(true); Load finalized = f.load();
                check(finalized.args[1] == period && finalized.from() == current && finalized.end() == current,
                    "rollover discards forming aggregate and re-fetches final native target candle");
                check(barEvents(f).size() == 1 && !state(f, "live"), "forming context cannot certify a newly closed bucket");
                emit(finalized, current, 1.01, 1.25, .98, 1.18, 4.5); finalized.finish(true); Load next = f.load();
                JsonObject finalEvent = barEvents(f).get(barEvents(f).size() - 1), finalBar = finalEvent.getAsJsonArray("bars").get(0).getAsJsonObject();
                check(finalBar.get("close").getAsDouble() == 1.18 && finalBar.get("volume").getAsDouble() == 4.5
                    && finalBar.get("closed").getAsBoolean() && finalEvent.get("coverageTo").getAsLong() == (current + step) / 1000,
                    "only the refetched final native values advance coverage across rollover");
                f.strategy.get().onTick(Instrument.EURUSD, tick(rollover.get(), 1.30));
                finishSnapshot(f, interval, current + step, rollover.get(), next, false);
                await(() -> state(f, "live"), "new bucket reconciles from its own basic candles");
                check(lastBar(f).get("time").getAsLong() == (current + step) / 1000 && lastBar(f).get("close").getAsDouble() == 1.30
                    && !lastBar(f).get("closed").getAsBoolean(), "new bucket retains newer live tick and provisional status");
            }
            AtomicLong rpcClock = new AtomicLong(current + step - 1_000);
            try (Fixture f = new Fixture(5_000, rpcClock::get)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current, current + step, 1), reply::complete);
                List<Window> windows = windows(interval, current, rpcClock.get());
                for (int i = 0; i < windows.size() - 1; i++) f.load().complete(true);
                Load minute = f.load(); emit(minute, minute.end(), 1.01, 1.04, .99, 1.03, 1.5);
                rpcClock.set(current + step + 1_000); minute.finish(true); Load finalized = f.load();
                check(finalized.args[1] == period && finalized.from() == current && finalized.end() == current && !reply.isDone(),
                    "RPC also refetches newly closed target rather than certifying stale aggregate");
                emit(finalized, current, 1.01, 1.25, .98, 1.18, 4.5); finalized.finish(true);
                JsonArray result = reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(result.size() == 1 && result.get(0).getAsJsonObject().get("close").getAsDouble() == 1.18
                    && result.get(0).getAsJsonObject().get("closed").getAsBoolean() && f.loads.isEmpty(),
                    "exclusive RPC end at rollover returns final native row without requesting next current candle");
            }
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                good(f.bridge, f.subscription("empty-current", interval)); f.load().complete(true);
                finishSnapshot(f, interval, current, NOW, null, true); await(() -> state(f, "live"), "empty successful context finishes");
                check(barEvents(f).size() == 1, "empty current component windows do not invent a candle");
            }
        }
        // Nonuniform OHLCV, callback ordering and interval boundaries must be
        // aggregated as real chronological candle values, not sums of prices.
        try (Fixture f = new Fixture(5_000, () -> NOW)) {
            long current = current("1w"); good(f.bridge, f.subscription("aggregation", "1w")); f.load().complete(true);
            Load daily = f.load();
            emit(daily, daily.end(), 100, 130, 90, 120, 2); emit(daily, daily.from(), 80, 110, 70, 100, 3); daily.finish(true);
            Load hourly = f.load(); emit(hourly, hourly.from(), 120, 145, 110, 140, 5); hourly.finish(true);
            Load minute = f.load(); emit(minute, minute.end(), 140, 155, 130, 150, 7); minute.finish(true);
            await(() -> state(f, "live"), "nonuniform candle aggregation completes"); JsonObject bar = lastBar(f);
            check(bar.get("open").getAsDouble() == 80 && bar.get("high").getAsDouble() == 155 && bar.get("low").getAsDouble() == 70
                && bar.get("close").getAsDouble() == 150 && bar.get("volume").getAsDouble() == 17,
                "source rows sort by timestamp, take first open/latest close/extremes, and sum native volume exactly once");
            f.strategy.get().onBar(Instrument.EURUSD, Period.WEEKLY, null, bar(current, 81, 156, 69, 151));
            int before = f.events.size(); f.strategy.get().onTick(Instrument.EURUSD, tick(NOW + 1000, 152));
            check(f.events.size() == before && lastBar(f).get("closed").getAsBoolean(), "a native close replaces context and cannot be reopened by late tick");
        }
        try (Fixture f = new Fixture(400, () -> NOW)) {
            good(f.bridge, f.subscription("current-timeout", "1d")); f.load().complete(true); Load hourly = f.load(); hourly.complete(true); Load minute = f.load();
            await(() -> retrying(f), "stalled final component reaches the shared bounded deadline");
            await(() -> minute.progress().stopJob(), "timeout cancels minute loader");
            check(barEvents(f).size() == 1 && !state(f, "live"), "partial context never publishes or advances closed coverage");
            int before = f.events.size(); minute.complete(true); check(f.events.size() == before, "timed-out component cannot publish late");
        }
        try (Fixture f = new Fixture(5_000, () -> NOW)) {
            good(f.bridge, f.subscription("delayed-tick", "1d")); f.load().complete(true); f.load().complete(true); Load minute = f.load();
            f.strategy.get().onTick(Instrument.EURUSD, tick(NOW - 60_000, 1.22));
            emit(minute, NOW, 1.25, 1.35, 1.24, 1.30, 3); minute.finish(true);
            await(() -> state(f, "live"), "newer minute context reconciles despite a delayed earlier quote");
            check(lastBar(f).get("close").getAsDouble() == 1.30 && lastBar(f).get("high").getAsDouble() == 1.35
                && lastBar(f).get("volume").getAsDouble() == 5.5,
                "a later callback from an earlier quote bucket cannot rewind the newer component close or volume");
            int before = f.events.size(); f.strategy.get().onTick(Instrument.EURUSD, tick(NOW - 30_000, 1.23));
            check(f.events.size() == before && lastBar(f).get("close").getAsDouble() == 1.30,
                "delayed ticks also respect the component freshness floor after publication");
            f.strategy.get().onTick(Instrument.EURUSD, tick(NOW + 1_000, 1.40));
            check(lastBar(f).get("close").getAsDouble() == 1.40 && lastBar(f).get("high").getAsDouble() == 1.40
                && lastBar(f).get("volume").getAsDouble() == 5.5,
                "a genuinely newer tick still updates the current candle and retains known source volume");
        }
        long saturday = Instant.parse("2026-10-31T12:34:00Z").toEpochMilli(), saturdayStart = Instant.parse("2026-10-31T00:00:00Z").toEpochMilli();
        try (Fixture f = new Fixture(5_000, () -> saturday)) {
            f.offline.set(true); good(f.bridge, f.subscription("closed-market", "1d")); f.load().complete(true);
            Load hourly = f.load(); hourly.finish(true); Load minute = f.load(); emit(minute, minute.end(), 1.1, 1.1, 1.1, 1.1, 0); minute.finish(true);
            await(() -> state(f, "marketClosed"), "closed market remains explicit");
            check(barEvents(f).size() == 1 && lastBar(f).get("time").getAsLong() < saturdayStart / 1000,
                "SDK current flat placeholder does not become a fabricated weekend candle");
        }
        try (Fixture f = new Fixture(5_000, () -> saturday)) {
            f.offline.set(true); good(f.bridge, f.subscription("closed-week", "1w")); f.load().complete(true);
            Load daily = f.load(); emit(daily, daily.end(), 1.2, 1.6, 1.1, 1.5, 9); daily.finish(true);
            f.load().finish(true); Load minute = f.load(); emit(minute, minute.end(), 1.1, 1.1, 1.1, 1.1, 0); minute.finish(true);
            await(() -> state(f, "marketClosed"), "weekly market closure stays explicit");
            check(lastBar(f).get("close").getAsDouble() == 1.5 && lastBar(f).get("volume").getAsDouble() == 9,
                "empty current-minute placeholder cannot rewind real completed daily data in the open week");
        }
        for (boolean unaligned : List.of(false, true)) try (Fixture f = new Fixture(5_000, () -> NOW)) {
            CompletableFuture<JsonObject> reply = new CompletableFuture<>();
            f.bridge.dispatch(history(f, "1d", current("1d"), NOW + 1000, 1), reply::complete); Load hourly = f.load();
            emit(hourly, hourly.from() + (unaligned ? 1000 : 0), unaligned ? 1.1 : -1, 1.2, .9, 1.1, 2); hourly.finish(true);
            check(!reply.get(2, TimeUnit.SECONDS).get("ok").getAsBoolean() && f.loads.isEmpty(),
                "invalid or unaligned source values cannot be hidden inside an otherwise valid aggregate");
        }
        for (String interval : List.of("1m", "1h", "4h")) {
            long step = Bridge.PERIODS.get(interval).getInterval(), end = Math.floorDiv(NOW, step) * step;
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>(); f.bridge.dispatch(history(f, interval, end - 2 * step, end, 500), reply::complete); Load gap = f.load();
                check(gap.args[1] == Bridge.PERIODS.get(interval) && gap.range() && gap.from() == end - 2 * step && gap.end() == end - step,
                    "requested intraday period continues to load only its own native candles");
                gap.complete(true); check(reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 1 && f.loads.isEmpty(), "intraday query does not prefetch other periods");
            }
        }
        System.out.println("JForex D1/W1 candle-first current context, native history, OHLCV, rollover, checkpoints and cancellation checks passed");
    }
}

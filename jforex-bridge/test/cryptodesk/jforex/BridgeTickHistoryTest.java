package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Offline IHistory interface fakes exercise the actual asynchronous file export. */
public final class BridgeTickHistoryTest {
    static final long FROM = java.time.Instant.parse("2024-01-01T00:00:00Z").toEpochMilli();
    static final long TO = FROM + 3_600_000;
    interface Ticks { List<ITick> get(Object[] args) throws Exception; }
    static ITick quote(long at, double ask, double bid, double askVolume, double bidVolume) {
        return fake(ITick.class, (method, args) -> switch (method) {
            case "getTime" -> at; case "getAsk" -> ask; case "getBid" -> bid;
            case "getAskVolume" -> askVolume; case "getBidVolume" -> bidVolume; default -> null;
        });
    }
    static final class Fixture implements AutoCloseable {
        final Path directory = Files.createTempDirectory("jforex-tick-export-");
        final Path output = directory.resolve("ticks.csv");
        final Set<Instrument> subscribed = ConcurrentHashMap.newKeySet();
        final AtomicInteger calls = new AtomicInteger();
        final AtomicBoolean connected = new AtomicBoolean();
        final AtomicReference<IStrategy> strategy = new AtomicReference<>();
        final List<JsonObject> events = new CopyOnWriteArrayList<>();
        final Bridge bridge;
        int sequence;
        Fixture(Ticks ticks, long timeout) throws Exception { this(ticks, timeout, timeout); }
        Fixture(Ticks ticks, long chartTimeout, long tickTimeout) throws Exception {
            Files.createFile(output); privateFile(output);
            IHistory history = fake(IHistory.class, (method, args) -> switch (method) {
                case "getTicks" -> {
                    check(args.length == 3 && args[0] == Instrument.BTCUSD && (Long) args[1] == FROM && (Long) args[2] == TO - 1,
                        "export uses exact native BTCUSD SDK instrument and inclusive end excludes next hour");
                    check(subscribed.contains(Instrument.BTCUSD), "SDK tick request retains its subscription");
                    calls.incrementAndGet(); yield ticks.get(args);
                }
                case "getBarStart" -> Math.floorDiv((Long) args[1], ((Period) args[0]).getInterval()) * ((Period) args[0]).getInterval();
                case "readBars" -> null;
                default -> null;
            });
            IContext context = fake(IContext.class, (method, args) -> switch (method) {
                case "getHistory" -> history;
                case "getSubscribedInstruments" -> new HashSet<>(subscribed);
                case "setSubscribedInstruments" -> { check(Boolean.FALSE.equals(args[1]), "history does not block control subscription"); subscribed.clear(); subscribed.addAll((Set<Instrument>) args[0]); yield null; }
                case "getEngine" -> throw new AssertionError("historical export must never access broker orders");
                default -> null;
            });
            IClient client = fake(IClient.class, (method, args) -> switch (method) {
                case "connect" -> { connected.set(true); yield null; }
                case "isConnected" -> connected.get();
                case "getAvailableInstruments" -> Set.of(Instrument.BTCUSD);
                case "getSubscribedInstruments" -> new HashSet<>(subscribed);
                case "unsubscribeInstruments" -> { subscribed.removeAll((Set<Instrument>) args[0]); yield null; }
                case "startStrategy" -> { strategy.set((IStrategy) args[0]); strategy.get().onStart(context); yield 1L; }
                case "disconnect" -> { connected.set(false); yield null; }
                default -> null;
            });
            bridge = new Bridge(() -> client, events::add, chartTimeout, () -> TO + 60_000, 2_000, tickTimeout);
            JsonObject login = request("connect"); login.addProperty("username", "offline-tick-user"); login.addProperty("password", "offline-tick-password");
            login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", directory.toString()); good(bridge, login);
        }
        JsonObject export() {
            JsonObject request = request("historyTicks"); request.addProperty("exportId", "export-" + ++sequence);
            request.addProperty("instrument", "BTCUSD"); request.addProperty("from", FROM); request.addProperty("to", TO); request.addProperty("output", output.toString()); return request;
        }
        CompletableFuture<JsonObject> dispatch(JsonObject request) {
            CompletableFuture<JsonObject> reply = new CompletableFuture<>(); bridge.dispatch(request, reply::complete); return reply;
        }
        JsonObject finish(JsonObject request) throws Exception { return dispatch(request).get(3, TimeUnit.SECONDS); }
        void cancel(JsonObject export) { JsonObject request = request("historyTicksCancel"); request.add("exportId", export.get("exportId")); good(bridge, request); }
        public void close() throws Exception {
            bridge.close();
            try (var files = Files.walk(directory)) { for (Path path : files.sorted(Comparator.reverseOrder()).toList()) Files.deleteIfExists(path); }
        }
    }
    static void privateFile(Path path) throws Exception {
        if (Files.getFileAttributeView(path, java.nio.file.attribute.PosixFileAttributeView.class) != null)
            Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"));
    }
    static void failed(JsonObject response, String reason) { check(!response.get("ok").getAsBoolean(), reason + ": " + response); }

    static void queuedHistoryBudget() throws Exception {
        CountDownLatch occupied = new CountDownLatch(2), unblock = new CountDownLatch(1);
        CountDownLatch timerEntered = new CountDownLatch(1), releaseTimers = new CountDownLatch(1);
        AtomicInteger queuedReads = new AtomicInteger();
        try (Fixture f = new Fixture(values -> {
            if (queuedReads.incrementAndGet() <= 2) {
                occupied.countDown();
                // A native cache load may finish after the outer deadline.
                while (unblock.getCount() != 0) try { unblock.await(); } catch (InterruptedException ignored) { }
            }
            return List.of(quote(FROM, 2, 1, 1, 1));
        }, 50, 200)) {
            try {
                var field = Bridge.class.getDeclaredField("deadlines"); field.setAccessible(true);
                ScheduledThreadPoolExecutor timers = (ScheduledThreadPoolExecutor) field.get(f.bridge);
                // Start the 200 ms deadlines normally, but let both workers
                // reach the SDK before a busy runner can deliver them.
                timers.execute(() -> {
                    timerEntered.countDown();
                    try { releaseTimers.await(); } catch (InterruptedException interrupted) { Thread.currentThread().interrupt(); }
                });
                check(timerEntered.await(2, TimeUnit.SECONDS), "fixture deadline scheduler is held before dispatch");
                CompletableFuture<JsonObject> first = f.dispatch(f.export()), second = f.dispatch(f.export());
                check(occupied.await(2, TimeUnit.SECONDS), "both bounded SDK workers entered; first=" + first.getNow(null)
                    + ", second=" + second.getNow(null) + ", SDK reads=" + queuedReads.get());
                CompletableFuture<JsonObject> queued = f.dispatch(f.export());
                releaseTimers.countDown();
                for (JsonObject response : List.of(first.get(2, TimeUnit.SECONDS), second.get(2, TimeUnit.SECONDS))) {
                    failed(response, "stalled hour has explicit timeout");
                    JsonObject error = response.getAsJsonObject("error");
                    check(error.get("kind").getAsString().equals("timeout")
                        && error.get("message").getAsString().contains("JForex tick history loading timed out")
                        && error.get("message").getAsString().contains("Completed quote hours are retained"), "stalled tick timeout stays typed and resumable: " + response);
                }
                // This same-executor sentinel runs after a full queued budget.
                // A submission-started deadline must have fired before it.
                timers.schedule(() -> { }, 200, TimeUnit.MILLISECONDS).get(2, TimeUnit.SECONDS);
                check(!queued.isDone() && queuedReads.get() == 2, "queued hour does not spend its SDK budget before executor entry");
                unblock.countDown(); JsonObject result = queued.get(2, TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean() && result.getAsJsonObject("result").get("rows").getAsLong() == 1 && queuedReads.get() == 3,
                    "queued hour succeeds when workers resume, with no duplicate SDK attempts or fabricated rows");
                check(Files.readString(f.output).equals("timestamp,askPrice,bidPrice,askVolume,bidVolume\n" + FROM + ",2.0,1.0,1.0,1.0\n"),
                    "only the successful queued read publishes the exact one-row file");
                await(f.subscribed::isEmpty, "stalled and queued loads release every subscription reference");
            } finally { releaseTimers.countDown(); unblock.countDown(); }
        }
    }

    public static void main(String[] args) throws Exception {
        try (Fixture f = new Fixture(values -> List.of(quote(FROM, 11, 10, 0, 2), quote(FROM, 12, 11, 3, 4), quote(TO - 1, 13, 12, 5, 6)), 5_000)) {
            JsonObject result = f.finish(f.export());
            check(result.get("ok").getAsBoolean() && result.getAsJsonObject("result").get("rows").getAsLong() == 3, "bounded file response reports real rows");
            check(Files.readString(f.output).equals("timestamp,askPrice,bidPrice,askVolume,bidVolume\n" + FROM + ",11.0,10.0,0.0,2.0\n" + FROM + ",12.0,11.0,3.0,4.0\n" + (TO - 1) + ",13.0,12.0,5.0,6.0\n"),
                "actual BID/ASK tick values and duplicate milliseconds are preserved in order");
            check(!result.toString().contains("askPrice"), "ticks do not travel through capped JSON IPC");
            await(f.subscribed::isEmpty, "completed tick export releases its subscription");
            check(good(f.bridge, request("version")).getAsJsonObject("result").get("tickHistory").getAsString().equals("bid-hour-file-v1"), "capability is advertised");
        }
        try (Fixture f = new Fixture(values -> List.of(), 5_000)) {
            JsonObject result = f.finish(f.export());
            check(result.get("ok").getAsBoolean() && result.getAsJsonObject("result").get("rows").getAsLong() == 0
                && Files.readString(f.output).equals("timestamp,askPrice,bidPrice,askVolume,bidVolume\n"), "successful empty hour is explicit, not a missing-file success");
        }
        for (String invalid : List.of("nonempty", "symlink", "relative", "unaligned", "two-hours", "forming", "unknown", "permissions")) {
            try (Fixture f = new Fixture(values -> { throw new AssertionError("invalid request reached SDK history"); }, 5_000)) {
                JsonObject request = f.export(); Path target = f.directory.resolve("retained");
                switch (invalid) {
                    case "nonempty" -> Files.writeString(f.output, "retained original");
                    case "symlink" -> { Files.writeString(target, "retained original"); Files.delete(f.output); Files.createSymbolicLink(f.output, target); }
                    case "relative" -> request.addProperty("output", "ticks.csv");
                    case "unaligned" -> { request.addProperty("from", FROM + 1); request.addProperty("to", TO + 1); }
                    case "two-hours" -> request.addProperty("from", FROM - 3_600_000);
                    case "forming" -> { request.addProperty("from", TO); request.addProperty("to", TO + 3_600_000); }
                    case "unknown" -> request.addProperty("instrument", "ETHUSD");
                    case "permissions" -> {
                        if (Files.getFileAttributeView(f.output, java.nio.file.attribute.PosixFileAttributeView.class) == null) continue;
                        Files.setPosixFilePermissions(f.output, PosixFilePermissions.fromString("rw-r--r--"));
                    }
                }
                failed(f.finish(request), "reject " + invalid); check(f.calls.get() == 0, "request bounds are checked before SDK access");
                if (invalid.equals("nonempty")) check(Files.readString(f.output).equals("retained original"), "existing file preserved");
                if (invalid.equals("symlink")) check(Files.readString(target).equals("retained original"), "symlink target preserved");
            }
        }
        for (List<ITick> invalid : List.of(
                List.of(quote(FROM - 1, 2, 1, 1, 1)), List.of(quote(TO, 2, 1, 1, 1)),
                List.of(quote(FROM + 1, 2, 1, 1, 1), quote(FROM, 2, 1, 1, 1)),
                List.of(quote(FROM, Double.NaN, 1, 1, 1)), List.of(quote(FROM, 2, 0, 1, 1)),
                List.of(quote(FROM, 2, 1, -1, 1)), List.of(quote(FROM, 2, 1, 1, Double.POSITIVE_INFINITY)),
                Collections.nCopies(1_000_001, quote(FROM, 2, 1, 1, 1)))) {
            try (Fixture f = new Fixture(values -> invalid, 5_000)) { failed(f.finish(f.export()), "invalid provider data cannot publish"); await(f.subscribed::isEmpty, "failure releases SDK subscription"); }
        }
        try (Fixture f = new Fixture(values -> Collections.nCopies(1_000_000, quote(FROM, Double.MAX_VALUE, Double.MAX_VALUE, Double.MAX_VALUE, Double.MAX_VALUE)), 15_000)) {
            failed(f.dispatch(f.export()).get(20, TimeUnit.SECONDS), "64 MiB output budget enforced");
            check(Files.size(f.output) <= 64L * 1024 * 1024, "oversized response never writes past byte limit");
        }
        CountDownLatch slowEntered = new CountDownLatch(1), finishSlow = new CountDownLatch(1);
        try (Fixture f = new Fixture(values -> { slowEntered.countDown(); finishSlow.await(); return List.of(quote(FROM, 2, 1, 1, 1)); }, 50, 2_000)) {
            CompletableFuture<JsonObject> tick = f.dispatch(f.export()); check(slowEntered.await(2, TimeUnit.SECONDS), "slow SDK tick hour started");
            JsonObject history = request("history"); history.add("feedKey", feed(Instrument.BTCUSD, "cfd"));
            history.addProperty("from", FROM / 1000); history.addProperty("to", TO / 1000); history.addProperty("limit", 3);
            JsonObject chart = f.dispatch(history).get(2, TimeUnit.SECONDS);
            failed(chart, "chart keeps its own short bounded deadline");
            check(chart.getAsJsonObject("error").get("message").getAsString().contains("historical data loading timed out") && !tick.isDone(),
                "a genuine tick hour can outlast chart history budget without being discarded");
            finishSlow.countDown(); JsonObject result = tick.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonObject("result").get("rows").getAsLong() == 1,
                "slow successful SDK tick hour is exported exactly once");
        } finally { finishSlow.countDown(); }
        queuedHistoryBudget();
        CountDownLatch entered = new CountDownLatch(1), release = new CountDownLatch(1);
        try (Fixture f = new Fixture(values -> {
            entered.countDown();
            // Model an SDK implementation that returns after interruption.
            while (release.getCount() != 0) try { release.await(); } catch (InterruptedException ignored) { }
            return List.of(quote(FROM, 2, 1, 1, 1));
        }, 5_000)) {
            JsonObject export = f.export(); CompletableFuture<JsonObject> reply = f.dispatch(export);
            check(entered.await(2, TimeUnit.SECONDS), "tick read entered");
            check(good(f.bridge, request("version")).get("ok").getAsBoolean(), "control commands remain responsive while getTicks blocks");
            JsonObject subscribe = request("subscribe"); subscribe.addProperty("subscriptionId", "live"); subscribe.add("feedKey", feed(Instrument.BTCUSD, "cfd")); good(f.bridge, subscribe);
            f.strategy.get().onTick(Instrument.BTCUSD, quote(TO + 1000, 3, 2, 1, 1));
            check(f.events.stream().anyMatch(event -> event.has("bars")), "live quotes remain responsive during blocked tick export");
            f.cancel(export); failed(reply.get(2, TimeUnit.SECONDS), "explicit cancellation completes with no exported data");
            release.countDown(); await(() -> f.calls.get() == 1, "one SDK read only");
            JsonObject unsubscribe = request("unsubscribe"); unsubscribe.addProperty("subscriptionId", "live"); good(f.bridge, unsubscribe);
            await(f.subscribed::isEmpty, "late canceled read releases only its own subscription");
            check(Files.size(f.output) == 0, "late SDK response cannot open canceled output");
            JsonObject early = f.export(); f.cancel(early); failed(f.finish(early), "pre-start cancellation is honored"); check(f.calls.get() == 1, "pre-start cancellation never reaches SDK");
        } finally { release.countDown(); }
        CountDownLatch timedRead = new CountDownLatch(1);
        AtomicInteger timeoutAttempts = new AtomicInteger();
        try (Fixture f = new Fixture(values -> {
            if (timeoutAttempts.incrementAndGet() == 1) { timedRead.countDown(); new CountDownLatch(1).await(); }
            return List.of(quote(FROM, 2, 1, 1, 1));
        }, 200)) {
            CompletableFuture<JsonObject> reply = f.dispatch(f.export()); check(timedRead.await(2, TimeUnit.SECONDS), "stalled history started");
            JsonObject result = reply.get(2, TimeUnit.SECONDS); failed(result, "stalled history has bounded deadline");
            check(result.getAsJsonObject("error").get("kind").getAsString().equals("timeout"), "deadline remains typed");
            check(result.getAsJsonObject("error").get("message").getAsString().contains("JForex tick history loading timed out")
                && result.getAsJsonObject("error").get("message").getAsString().contains("Completed quote hours are retained"), "resumable tick failure has its own actionable message");
            await(f.subscribed::isEmpty, "deadline cancels and releases historical subscription"); check(Files.size(f.output) == 0, "timeout never publishes a partial SDK read");
            JsonObject retry = f.finish(f.export());
            check(retry.get("ok").getAsBoolean() && retry.getAsJsonObject("result").get("rows").getAsLong() == 1
                && timeoutAttempts.get() == 2 && f.connected.get(), "an explicit same-hour retry reuses the healthy SDK session after timeout");
        }
        System.out.println("JForex bounded tick export, native source identity, file/row/time/value limits, cancellation and responsiveness checks passed");
    }
}

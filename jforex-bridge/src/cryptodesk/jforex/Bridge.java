package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.instrument.IFinancialInstrument;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.io.*;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.*;

/** Read-only JForex IClient transport. It never obtains an engine or places orders. */
public final class Bridge implements AutoCloseable {
    static final Gson JSON = new GsonBuilder().disableHtmlEscaping().create();
    static final Map<String, Period> PERIODS = Map.of("1m", Period.ONE_MIN, "5m", Period.FIVE_MINS,
        "15m", Period.FIFTEEN_MINS, "1h", Period.ONE_HOUR, "4h", Period.FOUR_HOURS,
        "1d", Period.DAILY, "1w", Period.WEEKLY);
    static final List<String> INTERVALS = List.of("1m", "5m", "15m", "1h", "4h", "1d", "1w");
    static final int MAX_LINE = 65536;
    static final int SEED_RANGE_BARS = 1000;
    private final Supplier<IClient> factory;
    private final Consumer<JsonObject> output;
    private final Map<String, Subscription> subscriptions = new ConcurrentHashMap<>();
    private final AtomicLong revision = new AtomicLong();
    private final AtomicBoolean reconnecting = new AtomicBoolean();
    private final ScheduledExecutorService reconnect = Executors.newSingleThreadScheduledExecutor(r -> daemon(r, "jforex-reconnect"));
    private final ScheduledThreadPoolExecutor deadlines = new ScheduledThreadPoolExecutor(1, r -> daemon(r, "jforex-deadlines"));
    private final ExecutorService data = new ThreadPoolExecutor(2, 2, 0, TimeUnit.MILLISECONDS,
        new ArrayBlockingQueue<>(32), r -> daemon(r, "jforex-history"));
    private final Set<DataJob> dataJobs = ConcurrentHashMap.newKeySet();
    private final Object instrumentLock = new Object();
    private final Map<Instrument, Integer> historyUsers = new HashMap<>();
    private final Set<Instrument> requestedInstruments = new HashSet<>();
    private final long historyTimeoutMillis;
    private final LongSupplier nowMillis;
    private volatile IClient client;
    private volatile IContext context;
    private volatile boolean closing;
    private volatile boolean connectedOnce;
    private volatile CountDownLatch strategyReady;
    private volatile Set<Instrument> available = Set.of();
    private long strategyId = -1;
    final IStrategy strategy = new IStrategy() {
        public void onStart(IContext value) { context = value; strategyReady.countDown(); }
        public void onTick(Instrument instrument, ITick tick) {
            if (closing || context == null) return;
            for (Subscription sub : subscriptions.values()) {
                if (!sub.instrument.equals(instrument)) continue;
                try {
                    // getBar(..., 0) can wait for the SDK's cold HTTP cache.
                    // Quote callbacks only use pure UTC bucketing and real ticks.
                    long start = context.getHistory().getBarStart(sub.period, tick.getTime());
                    synchronized (sub) {
                        if (start < sub.lastTime || tick.getTime() < sub.lastTickTime
                                || sub.last != null && start == sub.lastTime && sub.last.get("closed").getAsBoolean()) continue;
                        JsonObject bar = tickCandle(sub, tick, start);
                        sub.lastTickTime = tick.getTime(); sub.lastTime = start; sub.last = bar;
                        emitBars(sub, List.of(bar));
                        if (sub.seedError != null) health(sub.id, "error", sub.seedError, tick.getTime());
                        else if (sub.loading) health(sub.id, "reconciling", "Receiving JForex Bid ticks; loading historical candles", tick.getTime());
                        else health(sub.id, "live", "Receiving JForex Bid ticks", tick.getTime());
                    }
                } catch (Exception ignored) { health(sub.id, "error", "JForex returned an invalid quote", 0); }
            }
        }
        public void onBar(Instrument instrument, Period period, IBar ask, IBar bid) {
            if (closing || bid == null) return;
            for (Subscription sub : subscriptions.values()) {
                if (sub.instrument.equals(instrument) && sub.period.equals(period)) {
                    try {
                        synchronized (sub) {
                            JsonObject bar = candle(bid, true);
                            if (bid.getTime() >= sub.lastTime) { sub.lastTime = bid.getTime(); sub.last = bar; }
                            long coverageFrom = 0, coverageTo = 0;
                            if (!sub.loading && sub.seedError == null && sub.historyFrom > 0 && bid.getTime() >= sub.historyFrom
                                    && context != null && bid.getTime() + period.getInterval() <= context.getHistory().getBarStart(period, nowMillis.getAsLong())) {
                                // A healthy uninterrupted native stream extends
                                // the proven seed frontier, including closures.
                                // During catch-up/failure a later bar proves no
                                // coverage of the still-missing interior data.
                                coverageFrom = sub.historyFrom; coverageTo = bid.getTime() + period.getInterval();
                                sub.historyFrom = bid.getTime();
                            }
                            emitBars(sub, List.of(bar), coverageFrom, coverageTo);
                        }
                    }
                    catch (Exception ignored) { health(sub.id, "error", "JForex returned an invalid candle", 0); }
                }
            }
        }
        public void onMessage(IMessage ignored) { }
        public void onAccount(IAccount ignored) { }
        public void onStop() { context = null; if (!closing) allHealth("offline", "JForex data strategy stopped"); }
    };

    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output) {
        this(factory, output, 45_000);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis) {
        this(factory, output, historyTimeoutMillis, System::currentTimeMillis);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis) {
        this.factory = factory; this.output = output;
        this.historyTimeoutMillis = historyTimeoutMillis; this.nowMillis = nowMillis;
        deadlines.setRemoveOnCancelPolicy(true);
        reconnect.scheduleWithFixedDelay(this::heartbeat, 15, 15, TimeUnit.SECONDS);
    }
    private void heartbeat() {
        if (closing || client == null || !client.isConnected() || context == null) return;
        for (Subscription sub : subscriptions.values()) {
            try {
                if (sub.seedError != null) health(sub.id, "error", sub.seedError, 0);
                else if (sub.loading) health(sub.id, "reconciling", "Loading JForex Bid candles; live quotes remain subscribed", 0);
                else if (context.getDataService().isOfflineTime(nowMillis.getAsLong(), sub.instrument))
                    health(sub.id, "marketClosed", "JForex reports this forex market is closed", 0);
                else health(sub.id, "live", "Connected to JForex Bid feed", 0);
            } catch (Exception ignored) { health(sub.id, "live", "Connected to JForex Bid feed", 0); }
        }
    }

    // Only control work uses the command queue. A slow SDK HTTP history load
    // must never queue login checks, subscriptions or unsubscribe behind it.
    void dispatch(JsonObject request, Consumer<JsonObject> reply) {
        if (request.has("op") && "history".equals(request.get("op").getAsString())) {
            String id = request.has("id") ? request.get("id").getAsString() : "";
            AtomicBoolean replied = new AtomicBoolean();
            Consumer<JsonObject> once = response -> { if (replied.compareAndSet(false, true)) reply.accept(response); };
            submitData(() -> once.accept(handle(request)),
                message -> once.accept(failure(id, "timeout", message)));
        } else reply.accept(handle(request));
    }

    private DataJob submitData(Runnable action, Consumer<String> failed) {
        DataJob job = new DataJob(failed);
        dataJobs.add(job);
        job.task = new FutureTask<>(() -> {
            try { if (!job.finished.get()) action.run(); }
            finally { job.finish(); }
            return null;
        });
        job.timeout = deadlines.schedule(() -> job.fail("JForex historical data loading timed out. Live quotes can continue; retry the chart to reload history."), historyTimeoutMillis, TimeUnit.MILLISECONDS);
        try { data.execute(job.task); }
        catch (RejectedExecutionException error) { job.fail("JForex history request queue is full. Retry after the pending requests finish."); }
        return job;
    }

    private final class DataJob {
        final AtomicBoolean finished = new AtomicBoolean(); final Consumer<String> failed;
        volatile FutureTask<Void> task; volatile Future<?> timeout;
        DataJob(Consumer<String> failed) { this.failed = failed; }
        void finish() { if (finished.compareAndSet(false, true)) { if (timeout != null) timeout.cancel(false); dataJobs.remove(this); } }
        void fail(String message) { if (finished.compareAndSet(false, true)) { if (task != null) task.cancel(true); if (timeout != null) timeout.cancel(false); dataJobs.remove(this); failed.accept(message); } }
        void cancel() { fail("JForex historical data request was canceled."); }
    }

    JsonObject handle(JsonObject request) {
        String id = "";
        try {
            id = text(request, "id", 128);
            String op = text(request, "op", 32);
            Object result = switch (op) {
                case "connect" -> connect(request);
                case "captcha" -> captcha(request);
                case "version" -> Map.of("protocol", 1, "sdkVersion", "3.6.51", "javaVersion", Runtime.version().feature());
                case "instruments" -> instruments();
                case "history" -> history(request);
                case "subscribe" -> subscribe(request);
                case "unsubscribe" -> unsubscribe(text(request, "subscriptionId", 128));
                case "disconnect" -> { disconnect(); yield Map.of(); }
                case "close" -> { close(); yield Map.of(); }
                default -> throw new Failure("internal", "Unknown JForex bridge operation");
            };
            JsonObject response = new JsonObject(); response.addProperty("id", id); response.addProperty("ok", true);
            response.add("result", JSON.toJsonTree(result)); return response;
        } catch (Failure ex) { return failure(id, ex.kind, ex.getMessage()); }
        catch (JFAuthenticationException ex) {
            return failure(id, "authentication", "JForex rejected the credentials or this IP address. Verify the Demo/Live environment. Live accounts may require authorizing this IP in JForex before connecting.");
        } catch (JFVersionException ex) { return failure(id, "restricted", "This JForex SDK version was rejected by the server. Update CryptoDesk."); }
        catch (Exception ex) { return failure(id, "offline", "JForex could not complete the request. Check the connection, account permissions and JForex service availability."); }
    }

    private Object connect(JsonObject request) throws Exception {
        if (client != null && client.isConnected() && context != null) return Map.of("connected", true, "instrumentCount", available.size());
        String username = text(request, "username", 256), password = text(request, "password", 4096);
        String environment = text(request, "environment", 8);
        if (!environment.equals("demo") && !environment.equals("live")) throw new Failure("authentication", "Select the Demo or Live JForex environment");
        Path cache = Path.of(text(request, "cacheDirectory", 4096));
        if (!cache.isAbsolute()) throw new Failure("internal", "JForex cache directory must be absolute");
        Files.createDirectories(cache);
        closing = false;
        if (client == null) client = factory.get();
        client.setOut(new PrintStream(OutputStream.nullOutputStream()));
        client.setErr(new PrintStream(OutputStream.nullOutputStream()));
        client.setCacheDirectory(cache.toFile());
        client.setSystemListener(new ISystemListener() {
            public void onStart(long ignored) { }
            public void onStop(long ignored) { }
            public void onConnect() {
                if (connectedOnce) {
                    allHealth("reconciling", "JForex reconnected; refreshing recent candles");
                    reconnect.execute(() -> {
                        for (Subscription sub : subscriptions.values()) {
                            scheduleSeed(sub);
                        }
                    });
                }
            }
            public void onDisconnect() {
                if (closing) return;
                for (Subscription sub : subscriptions.values()) sub.loading = true;
                allHealth("offline", "JForex connection interrupted");
                if (reconnecting.compareAndSet(false, true)) reconnect.schedule(Bridge.this::tryReconnect, 5, TimeUnit.SECONDS);
            }
        });
        // Credentials exist only in this stdin request and the SDK's authenticated session.
        // Never send them in command arguments, environment variables or diagnostic messages.
        String endpoint = "https://platform.dukascopy.com/" + environment + "_3/jforex_3.jnlp";
        String pin = request.has("pin") && !request.get("pin").isJsonNull() ? request.get("pin").getAsString() : "";
        if (pin.length() > 128) throw new Failure("authentication", "Invalid JForex PIN");
        // A previous login can authenticate successfully while data-strategy
        // startup fails. Retry that startup on the existing authenticated SDK
        // session rather than attempting a second login (and invalidating PIN).
        if (!client.isConnected()) {
            if (pin.isBlank()) client.connect(endpoint, username, password);
            else client.connect(endpoint, username, password, pin);
        }
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(35);
        while (!client.isConnected() && System.nanoTime() < deadline && !closing) Thread.sleep(100);
        if (!client.isConnected()) throw new Failure("offline", "JForex connection timed out. Check credentials, account environment and network access.");
        TreeSet<Instrument> forex = new TreeSet<>(Comparator.comparing(Instrument::name));
        for (Instrument instrument : client.getAvailableInstruments()) {
            if (isFiatForex(instrument)) forex.add(instrument);
        }
        available = Collections.unmodifiableSet(forex);
        strategyReady = new CountDownLatch(1);
        strategyId = client.startStrategy(strategy, (pid, source, error) -> allHealth("error", "JForex data strategy failed"));
        if (!strategyReady.await(15, TimeUnit.SECONDS) || context == null) throw new Failure("offline", "JForex data strategy did not become ready");
        connectedOnce = true;
        return Map.of("connected", true, "instrumentCount", available.size());
    }

    private Object captcha(JsonObject request) throws Exception {
        if (client != null && client.isConnected()) throw new Failure("restricted", "Disconnect the JForex session before requesting a new login challenge");
        Path cache = Path.of(text(request, "cacheDirectory", 4096));
        if (!cache.isAbsolute()) throw new Failure("internal", "JForex cache directory must be absolute");
        Files.createDirectories(cache);
        if (client == null) client = factory.get();
        client.setOut(new PrintStream(OutputStream.nullOutputStream()));
        client.setErr(new PrintStream(OutputStream.nullOutputStream()));
        client.setCacheDirectory(cache.toFile());
        java.awt.image.BufferedImage image = client.getCaptchaImage("https://platform.dukascopy.com/live_3/jforex_3.jnlp");
        if (image == null || image.getWidth() > 4096 || image.getHeight() > 4096)
            throw new Failure("restricted", "JForex did not provide a valid login challenge");
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        if (!javax.imageio.ImageIO.write(image, "png", bytes) || bytes.size() > 2 * 1024 * 1024)
            throw new Failure("restricted", "JForex login challenge is too large");
        return Map.of("imageDataUrl", "data:image/png;base64," + Base64.getEncoder().encodeToString(bytes.toByteArray()));
    }

    private void tryReconnect() {
        try {
            if (closing || client == null || client.isConnected()) return;
            if (!client.isReconnectAllowed()) { allHealth("offline", "JForex session expired. Reconnect from Settings → Dukascopy."); return; }
            allHealth("reconciling", "Reconnecting JForex session");
            client.reconnect();
        } catch (Exception ignored) { allHealth("offline", "JForex reconnect failed"); }
        finally {
            reconnecting.set(false);
            if (!closing && client != null && !client.isConnected() && client.isReconnectAllowed() && reconnecting.compareAndSet(false, true))
                reconnect.schedule(this::tryReconnect, 15, TimeUnit.SECONDS);
        }
    }

    static boolean isFiatForex(Instrument instrument) {
        if (instrument.getType() != IFinancialInstrument.Type.FOREX) return false;
        try {
            Currency.getInstance(instrument.getPrimaryJFCurrency().getCurrencyCode());
            Currency.getInstance(instrument.getSecondaryJFCurrency().getCurrencyCode());
            return true;
        } catch (IllegalArgumentException ignored) { return false; }
    }

    private Object instruments() {
        requireConnected();
        List<Object> result = new ArrayList<>();
        for (Instrument instrument : available) {
            result.add(Map.of("nativeId", instrument.name(), "market", "forex",
                "baseAsset", instrument.getPrimaryJFCurrency().getCurrencyCode(), "quoteAsset", instrument.getSecondaryJFCurrency().getCurrencyCode(),
                "priceIncrement", BigDecimal.ONE.movePointLeft(instrument.getTickScale()).toPlainString(),
                "sizeIncrement", "", "availability", "available", "supportedIntervals", INTERVALS,
                "providerMetadata", Map.of("priceSide", "bid", "volumeUnit", "millions", "sdkSymbol", instrument.toString())));
        }
        return result;
    }

    private List<JsonObject> history(JsonObject request) throws Exception {
        requireConnected();
        Feed feed = feed(request.getAsJsonObject("feedKey"));
        int limit = integer(request, "limit", 1, 5000);
        long from = optionalLong(request, "from"), to = optionalLong(request, "to");
        if (from < 0 || to < 0 || from > 32503680000L || to > 32503680000L || (from > 0 && to > 0 && from >= to))
            throw new Failure("internal", "Invalid JForex history range");
        synchronized (instrumentLock) { historyUsers.merge(feed.instrument, 1, Integer::sum); }
        try {
            ensureInstrument(feed.instrument);
            IHistory history = context.getHistory();
            long until = to > 0 ? to * 1000 - 1 : nowMillis.getAsLong();
            long end = history.getBarStart(feed.period, until);
            // Count-based loading crosses weekends without synthesizing candles.
            List<IBar> source = readBars(history, feed.instrument, feed.period, limit, end);
            long current = history.getBarStart(feed.period, nowMillis.getAsLong());
            TreeMap<Long, JsonObject> result = new TreeMap<>();
            for (IBar bar : source) {
                if (inactiveFlat(feed.instrument, bar, current)) continue;
                if (bar.getTime() / 1000 < from || (to > 0 && bar.getTime() / 1000 >= to)) continue;
                result.put(bar.getTime(), candle(bar, bar.getTime() < current));
            }
            return new ArrayList<>(result.values());
        } finally {
            synchronized (instrumentLock) {
                historyUsers.computeIfPresent(feed.instrument, (instrument, count) -> count == 1 ? null : count - 1);
                releaseInstruments();
            }
        }
    }

    private Object subscribe(JsonObject request) throws Exception {
        requireConnected();
        String id = text(request, "subscriptionId", 128);
        Feed feed = feed(request.getAsJsonObject("feedKey"));
        if (subscriptions.size() >= 64 && !subscriptions.containsKey(id)) throw new Failure("restricted", "Too many JForex subscriptions");
        long historyFrom = optionalLong(request, "historyFrom");
        if (historyFrom < 0 || historyFrom > nowMillis.getAsLong() / 1000)
            throw new Failure("internal", "Invalid JForex cached history anchor");
        Subscription sub = new Subscription(id, feed.instrument, feed.period);
        sub.historyFrom = historyFrom * 1000;
        if (subscriptions.putIfAbsent(id, sub) != null) throw new Failure("internal", "Duplicate JForex subscription");
        try {
            ensureInstrument(feed.instrument);
            health(id, "reconciling", "Loading JForex Bid candles", 0);
            scheduleSeed(sub);
            return Map.of("subscribed", true);
        } catch (Exception error) { subscriptions.remove(id, sub); releaseInstruments(); throw error; }
    }

    private void scheduleSeed(Subscription sub) {
        long generation = sub.seedGeneration.incrementAndGet();
        if (sub.seed != null) sub.seed.cancel();
        sub.loading = true; sub.seedError = null;
        sub.seed = submitData(() -> {
            try { seed(sub, generation); }
            catch (Exception ignored) { if (!Thread.currentThread().isInterrupted()) seedFailed(sub, generation, "JForex historical data could not be loaded. Live quotes remain subscribed; retry the chart to reload history."); }
        }, message -> seedFailed(sub, generation, message));
    }
    private void seedFailed(Subscription sub, long generation, String message) {
        synchronized (sub) {
            if (subscriptions.get(sub.id) != sub || closing || !sub.seedGeneration.compareAndSet(generation, generation + 1)) return;
            sub.loading = false; sub.seedError = message; health(sub.id, "error", message, 0);
        }
    }
    private void seed(Subscription sub, long generation) throws Exception {
        IHistory history = context.getHistory();
        long seedRevision = nextRevision();
        long end = history.getBarStart(sub.period, nowMillis.getAsLong());
        long from = sub.historyFrom;
        if (from == 0) {
            publishSeed(sub, generation, seedRevision, readBars(history, sub.instrument, sub.period, 500, end), 0, end, true);
            return;
        }
        // Account-scoped local candles already supply the old history. Fetch
        // only the overlap and missing suffix, bounded per SDK request/event.
        // Empty successful chunks are valid forex market closures.
        from = history.getBarStart(sub.period, from);
        while (from <= end) {
            if (closing || Thread.currentThread().isInterrupted() || sub.seedGeneration.get() != generation) throw new InterruptedException();
            long chunkEnd = Math.min(end, from + (SEED_RANGE_BARS - 1L) * sub.period.getInterval());
            List<IBar> source = readRangeBars(history, sub.instrument, sub.period, from, chunkEnd);
            if (!publishSeed(sub, generation, seedRevision, source, from, chunkEnd, chunkEnd == end)) return;
            from = chunkEnd + sub.period.getInterval();
        }
    }

    private boolean publishSeed(Subscription sub, long generation, long seedRevision, List<IBar> source, long requestedFrom, long checkedThrough, boolean finished) throws Exception {
        long current = context.getHistory().getBarStart(sub.period, nowMillis.getAsLong());
        long coverageFrom = requestedFrom;
        if (coverageFrom == 0 && !source.isEmpty()) coverageFrom = source.get(0).getTime();
        long coverageTo = Math.min(checkedThrough + sub.period.getInterval(), current);
        TreeMap<Long, JsonObject> bars = new TreeMap<>();
        for (IBar bar : source) if (!inactiveFlat(sub.instrument, bar, current)) {
            JsonObject value = candle(bar, bar.getTime() < current);
            // A late seed cannot supersede newer SDK ticks or closed-bar events.
            value.addProperty("sourceRevision", seedRevision); bars.put(bar.getTime(), value);
        }
        synchronized (sub) {
            if (subscriptions.get(sub.id) != sub || closing || sub.seedGeneration.get() != generation || Thread.currentThread().isInterrupted()) return false;
            if (finished) { sub.loading = false; sub.seedError = null; }
            sub.historyFrom = Math.max(sub.historyFrom, Math.min(checkedThrough, current - sub.period.getInterval()));
            if (!bars.isEmpty()) {
                if (sub.last != null && sub.lastTime >= bars.lastKey()
                        && (sub.lastTime > bars.lastKey() || sub.last.get("sourceRevision").getAsLong() > seedRevision)) {
                    JsonObject previous = bars.get(sub.lastTime);
                    if (previous != null && !previous.get("closed").getAsBoolean() && !sub.last.get("closed").getAsBoolean()) {
                        // Recover the real opening context/known SDK volume while
                        // retaining quotes received during the asynchronous load.
                        JsonObject merged = previous.deepCopy();
                        merged.addProperty("high", Math.max(previous.get("high").getAsDouble(), sub.last.get("high").getAsDouble()));
                        merged.addProperty("low", Math.min(previous.get("low").getAsDouble(), sub.last.get("low").getAsDouble()));
                        merged.addProperty("close", sub.last.get("close").getAsDouble());
                        merged.addProperty("quality", "provisional"); merged.addProperty("sourceRevision", nextRevision());
                        bars.put(sub.lastTime, merged);
                    } else if (previous == null || sub.last.get("closed").getAsBoolean()) bars.put(sub.lastTime, sub.last);
                }
                sub.lastTime = bars.lastKey(); sub.last = bars.lastEntry().getValue();
            }
            // Coverage describes the completed SDK query, never a later live
            // candle merged above. Empty weekend chunks still prove coverage.
            if (!bars.isEmpty() || coverageFrom > 0 && coverageFrom < coverageTo)
                emitBars(sub, new ArrayList<>(bars.values()), coverageFrom, coverageTo);
            if (finished) {
                if (context.getDataService().isOfflineTime(nowMillis.getAsLong(), sub.instrument))
                    health(sub.id, "marketClosed", "JForex reports this forex market is closed", 0);
                else health(sub.id, "live", "Connected to JForex Bid feed; waiting for market updates", 0);
            }
            return true;
        }
    }

    private List<IBar> readBars(IHistory history, Instrument instrument, Period period, int limit, long end) throws Exception {
        return loadBars(history, instrument, period, limit, 0, end, false);
    }
    private List<IBar> readRangeBars(IHistory history, Instrument instrument, Period period, long from, long end) throws Exception {
        return loadBars(history, instrument, period, SEED_RANGE_BARS, from, end, true);
    }
    private List<IBar> loadBars(IHistory history, Instrument instrument, Period period, int limit, long from, long end, boolean range) throws Exception {
        CompletableFuture<List<IBar>> result = new CompletableFuture<>();
        ConcurrentNavigableMap<Long, IBar> bars = new ConcurrentSkipListMap<>();
        AtomicBoolean canceled = new AtomicBoolean();
        LoadingDataListener listener = new LoadingDataListener() {
            public void newTick(Instrument ignored, long time, double ask, double bid, double askVolume, double bidVolume) { }
            public void newBar(Instrument item, Period interval, OfferSide side, long time, double open, double close, double low, double high, double volume) {
                if (canceled.get() || result.isDone()) return;
                if (!instrument.equals(item) || !period.equals(interval) || side != OfferSide.BID || range && (time < from || time > end)) {
                    result.completeExceptionally(new Failure("internal", "JForex returned invalid historical data")); return;
                }
                bars.put(time, new LoadedBar(time, open, high, low, close, volume));
                if (bars.size() > limit) result.completeExceptionally(new Failure("internal", "JForex historical data exceeded its requested limit"));
            }
        };
        LoadingProgressListener progress = new LoadingProgressListener() {
            public void dataLoaded(long start, long end, long current, String information) { }
            public void loadingFinished(boolean complete, long start, long end, long current) {
                if (canceled.get()) return;
                if (complete) result.complete(new ArrayList<>(bars.values()));
                else result.completeExceptionally(new Failure("offline", "JForex historical data loading failed or was canceled. Retry the chart; live quotes remain independent."));
            }
            public boolean stopJob() { return canceled.get() || closing || result.isCompletedExceptionally(); }
        };
        try {
            IContext activeContext = context;
            long readyBy = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(historyTimeoutMillis);
            while (activeContext != null && !activeContext.getSubscribedInstruments().contains(instrument)) {
                if (closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
                if (System.nanoTime() >= readyBy) throw new TimeoutException();
                Thread.sleep(10);
            }
            if (activeContext == null || closing) throw new InterruptedException();
            if (range) history.readBars(instrument, period, OfferSide.BID, Filter.WEEKENDS, from, end, listener, progress);
            else history.readBars(instrument, period, OfferSide.BID, Filter.WEEKENDS, limit, end, 0, listener, progress);
            return result.get(historyTimeoutMillis, TimeUnit.MILLISECONDS);
        } catch (TimeoutException error) {
            throw new Failure("timeout", "JForex historical data loading timed out. Retry the chart to reload history.");
        } catch (ExecutionException error) {
            if (error.getCause() instanceof Failure failure) throw failure;
            throw error;
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt(); throw error;
        } finally { canceled.set(true); }
    }

    private boolean inactiveFlat(Instrument instrument, IBar bar, long current) {
        // IHistory explicitly includes a current in-progress flat bar even with
        // WEEKENDS filtering. Do not invent a Saturday/current-closure candle.
        // Preserve actual partial-day bars (including Sunday opening sessions).
        if (bar.getTime() != current || bar.getVolume() != 0 || bar.getHigh() != bar.getLow()
                || bar.getOpen() != bar.getClose()) return false;
        return context.getDataService().isOfflineTime(nowMillis.getAsLong(), instrument);
    }

    private Object unsubscribe(String id) {
        Subscription sub = subscriptions.remove(id);
        if (sub != null && sub.seed != null) sub.seed.cancel();
        releaseInstruments();
        return Map.of();
    }
    private void releaseInstruments() {
        synchronized (instrumentLock) {
            if (client == null || !client.isConnected()) return;
            Set<Instrument> needed = new HashSet<>(historyUsers.keySet());
            for (Subscription sub : subscriptions.values()) needed.add(sub.instrument);
            Set<Instrument> unused = new HashSet<>(requestedInstruments);
            unused.addAll(client.getSubscribedInstruments()); unused.removeAll(needed);
            if (!unused.isEmpty()) client.unsubscribeInstruments(unused);
            requestedInstruments.removeAll(unused);
        }
    }
    private void ensureInstrument(Instrument instrument) {
        synchronized (instrumentLock) {
            requestedInstruments.add(instrument);
            // readBars owns cache readiness; control commands never wait for history.
            context.setSubscribedInstruments(new HashSet<>(requestedInstruments), false);
        }
    }
    private Feed feed(JsonObject value) {
        if (value == null || !"dukascopy".equals(text(value, "exchange", 32)) || !"forex".equals(text(value, "market", 32)))
            throw new Failure("unknownInstrument", "Select a Dukascopy forex instrument");
        String symbol = text(value, "instrument", 32), timeframe = text(value, "timeframe", 8);
        Instrument instrument = null;
        for (Instrument item : available) if (item.name().equals(symbol)) { instrument = item; break; }
        if (instrument == null) throw new Failure("unknownInstrument", "This forex pair is unavailable for the connected JForex account");
        Period period = PERIODS.get(timeframe);
        if (period == null) throw new Failure("unsupportedInterval", "Unsupported JForex chart period");
        return new Feed(instrument, period);
    }
    private void requireConnected() { if (closing || client == null || !client.isConnected() || context == null) throw new Failure("offline", "Connect JForex in Settings → Dukascopy first"); }
    private JsonObject candle(IBar bar, boolean closed) {
        double o = bar.getOpen(), h = bar.getHigh(), l = bar.getLow(), c = bar.getClose(), v = bar.getVolume();
        if (bar.getTime() <= 0 || bar.getTime() % 1000 != 0 || !finite(o, h, l, c, v) || o <= 0 || l <= 0 || c <= 0 || h < Math.max(o, c) || l > Math.min(o, c) || v < 0)
            throw new Failure("internal", "Invalid JForex candle");
        JsonObject out = new JsonObject(); out.addProperty("time", bar.getTime() / 1000);
        out.addProperty("open", o); out.addProperty("high", h); out.addProperty("low", l); out.addProperty("close", c); out.addProperty("volume", v);
        out.addProperty("closed", closed); out.addProperty("quality", closed ? "authoritative" : "provisional");
        out.addProperty("sourceRevision", nextRevision());
        return out;
    }
    private long nextRevision() { return revision.updateAndGet(previous -> Math.max(previous + 1, nowMillis.getAsLong())); }
    private JsonObject tickCandle(Subscription sub, ITick tick, long start) {
        double bid = tick.getBid();
        if (!Double.isFinite(bid) || bid <= 0) throw new Failure("internal", "Invalid JForex quote");
        JsonObject out;
        if (sub.last != null && sub.lastTime == start) {
            out = sub.last.deepCopy(); out.addProperty("high", Math.max(bid, out.get("high").getAsDouble())); out.addProperty("low", Math.min(bid, out.get("low").getAsDouble())); out.addProperty("close", bid);
        } else {
            out = new JsonObject(); out.addProperty("time", start / 1000); out.addProperty("open", bid); out.addProperty("high", bid); out.addProperty("low", bid); out.addProperty("close", bid); out.addProperty("volume", 0);
        }
        out.addProperty("closed", false); out.addProperty("quality", "provisional");
        out.addProperty("sourceRevision", nextRevision()); return out;
    }
    private void emitBars(Subscription sub, List<JsonObject> bars) { emitBars(sub, bars, 0, 0); }
    private void emitBars(Subscription sub, List<JsonObject> bars, long coverageFrom, long coverageTo) {
        if (subscriptions.get(sub.id) != sub || closing) return;
        JsonObject out = new JsonObject(); out.addProperty("event", "bars"); out.addProperty("subscriptionId", sub.id); out.add("bars", JSON.toJsonTree(bars));
        if (coverageFrom > 0 && coverageFrom < coverageTo) {
            out.addProperty("coverageFrom", coverageFrom / 1000); out.addProperty("coverageTo", coverageTo / 1000);
        }
        output.accept(out);
    }
    private void allHealth(String state, String reason) { for (String id : subscriptions.keySet()) health(id, state, reason, 0); }
    private void health(String id, String state, String reason, long tradeTime) {
        Subscription sub = subscriptions.get(id);
        if (sub == null || closing) return;
        JsonObject out = new JsonObject(); out.addProperty("event", "health"); out.addProperty("subscriptionId", id);
        JsonObject health = new JsonObject(); health.addProperty("state", state); health.addProperty("reason", reason); health.addProperty("lastMessageAt", nowMillis.getAsLong() / 1000);
        long lastTrade = Math.max(tradeTime, sub.lastTickTime);
        if (lastTrade > 0) health.addProperty("lastTradeAt", lastTrade / 1000);
        out.add("health", health); output.accept(out);
    }
    private void disconnect() {
        closing = true; subscriptions.clear(); available = Set.of(); context = null; connectedOnce = false;
        for (DataJob job : dataJobs) job.cancel();
        synchronized (instrumentLock) { historyUsers.clear(); requestedInstruments.clear(); }
        IClient old = client; client = null;
        if (old != null) { try { if (strategyId >= 0) old.stopStrategy(strategyId); } catch (Exception ignored) { } try { old.disconnect(); } catch (Exception ignored) { } }
        strategyId = -1;
    }
    public void close() { disconnect(); reconnect.shutdownNow(); deadlines.shutdownNow(); data.shutdownNow(); }
    static JsonObject failure(String id, String kind, String message) {
        JsonObject out = new JsonObject(); out.addProperty("id", id); out.addProperty("ok", false);
        JsonObject error = new JsonObject(); error.addProperty("kind", kind); error.addProperty("message", message); out.add("error", error); return out;
    }
    static String text(JsonObject object, String name, int max) {
        if (!object.has(name) || !object.get(name).isJsonPrimitive() || !object.getAsJsonPrimitive(name).isString()) throw new Failure("internal", "Invalid JForex request field: " + name);
        String result = object.get(name).getAsString();
        if (result.isEmpty() || result.length() > max || result.indexOf('\0') >= 0) throw new Failure("internal", "Invalid JForex request field: " + name);
        return result;
    }
    static long optionalLong(JsonObject object, String name) {
        try { return object.has(name) ? object.get(name).getAsBigDecimal().longValueExact() : 0; }
        catch (Exception error) { throw new Failure("internal", "Invalid JForex request field: " + name); }
    }
    static int integer(JsonObject object, String name, int min, int max) {
        long value = optionalLong(object, name);
        if (value < min || value > max) throw new Failure("internal", "Invalid JForex request field: " + name);
        return (int) value;
    }
    static boolean finite(double... values) { for (double value : values) if (!Double.isFinite(value)) return false; return true; }
    static Thread daemon(Runnable task, String name) { Thread thread = new Thread(task, name); thread.setDaemon(true); return thread; }
    static record Feed(Instrument instrument, Period period) { }
    static record LoadedBar(long time, double open, double high, double low, double close, double volume) implements IBar {
        public long getTime() { return time; } public double getOpen() { return open; }
        public double getHigh() { return high; } public double getLow() { return low; }
        public double getClose() { return close; } public double getVolume() { return volume; }
    }
    static final class Subscription {
        final String id; final Instrument instrument; final Period period; long lastTime; volatile long lastTickTime; JsonObject last;
        final AtomicLong seedGeneration = new AtomicLong(); volatile DataJob seed; volatile boolean loading; volatile String seedError; volatile long historyFrom;
        Subscription(String id, Instrument instrument, Period period) { this.id = id; this.instrument = instrument; this.period = period; }
    }
    static final class Failure extends RuntimeException {
        final String kind; Failure(String kind, String message) { super(message); this.kind = kind; }
    }

    public static void main(String[] args) {
        // Hold the original pipe exclusively. SDK logging/console output is discarded
        // before any SDK implementation is loaded, so it cannot corrupt JSON or leak auth.
        PrintWriter pipe = new PrintWriter(new OutputStreamWriter(System.out, StandardCharsets.UTF_8), true);
        System.setOut(new PrintStream(OutputStream.nullOutputStream())); System.setErr(new PrintStream(OutputStream.nullOutputStream()));
        System.setProperty("java.awt.headless", "true");
        System.setProperty("log4j.configuration", "cryptodesk-log4j.properties");
        Thread.setDefaultUncaughtExceptionHandler((thread, error) -> { });
        BlockingQueue<JsonObject> outbound = new ArrayBlockingQueue<>(2048);
        Consumer<JsonObject> send = value -> { if (!outbound.offer(value)) Runtime.getRuntime().halt(2); };
        Bridge bridge = new Bridge(() -> { try { return ClientFactory.getDefaultInstance(); } catch (Exception ignored) { throw new Failure("internal", "JForex SDK could not initialize. Verify Java 17 or later and the complete jforex-runtime folder."); } }, send);
        Thread writer = daemon(() -> { try { while (true) { pipe.println(JSON.toJson(outbound.take())); if (pipe.checkError()) Runtime.getRuntime().halt(0); } } catch (InterruptedException ignored) { } }, "jforex-stdout"); writer.start();
        ExecutorService commands = new ThreadPoolExecutor(1, 1, 0, TimeUnit.MILLISECONDS, new ArrayBlockingQueue<>(64), r -> daemon(r, "jforex-command"));
        try (Reader input = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8))) {
            StringBuilder line = new StringBuilder(); int character;
            while ((character = input.read()) >= 0) {
                if (character == '\n') {
                    String command = line.toString(); line.setLength(0);
                    commands.execute(() -> {
                        try {
                            JsonObject request = JsonParser.parseString(command).getAsJsonObject();
                            bridge.dispatch(request, send);
                        } catch (Exception ignored) { send.accept(failure("", "internal", "Invalid JForex JSON request")); }
                    });
                } else {
                    if (line.length() >= MAX_LINE) break;
                    if (character != '\r') line.append((char) character);
                }
            }
        } catch (Exception ignored) { }
        finally {
            daemon(() -> { try { Thread.sleep(5000); } catch (InterruptedException ignored) { } Runtime.getRuntime().halt(0); }, "jforex-owner-exit").start();
            commands.shutdownNow(); bridge.close();
        }
        // SDK transport threads may outlive disconnect; an owner pipe closing always
        // terminates the child. No detached bridge is allowed after CryptoDesk exits.
        System.exit(0);
    }
}

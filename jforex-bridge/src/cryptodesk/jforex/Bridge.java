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

/** JForex IClient transport. Trading requires a separate explicit confirmation request. */
public final class Bridge implements AutoCloseable {
    static final Gson JSON = new GsonBuilder().disableHtmlEscaping().create();
    static final Map<String, Period> PERIODS = Map.of("1m", Period.ONE_MIN, "5m", Period.FIVE_MINS,
        "15m", Period.FIFTEEN_MINS, "1h", Period.ONE_HOUR, "4h", Period.FOUR_HOURS,
        "1d", Period.DAILY, "1w", Period.WEEKLY);
    static final List<String> INTERVALS = List.of("1m", "5m", "15m", "1h", "4h", "1d", "1w");
    static final int MAX_LINE = 65536;
    static final int SEED_RANGE_BARS = 1000;
    // SDK predefined instruments have no group until account metadata loads.
    // Explicit provider crypto codes are a fallback, never an ISO-currency test:
    // offshore yuan (CNH), for example, is fiat despite having no Java Currency.
    // https://www.dukascopy.com/swiss/english/crypto/range-of-markets/
    private static final Set<String> CRYPTO_CURRENCIES = Set.of("BTC", "ETH", "LTC", "BCH", "XLM", "DSH", "TRX",
        "ADA", "UNI", "LNK", "AVE", "CMP", "YFI", "BAT");
    private final Supplier<IClient> factory;
    private final Consumer<JsonObject> output;
    private final Map<String, Subscription> subscriptions = new ConcurrentHashMap<>();
    private final AtomicLong revision = new AtomicLong();
    private final AtomicBoolean reconnecting = new AtomicBoolean();
    private final ScheduledExecutorService reconnect = Executors.newSingleThreadScheduledExecutor(r -> daemon(r, "jforex-reconnect"));
    private final ScheduledThreadPoolExecutor deadlines = new ScheduledThreadPoolExecutor(1, r -> daemon(r, "jforex-deadlines"));
    private final ExecutorService data = new ThreadPoolExecutor(2, 2, 0, TimeUnit.MILLISECONDS,
        new ArrayBlockingQueue<>(32), r -> daemon(r, "jforex-history"));
    private final ExecutorService trading = new ThreadPoolExecutor(1, 1, 0, TimeUnit.MILLISECONDS, new ArrayBlockingQueue<>(8), r -> daemon(r, "jforex-trading"));
    private final Set<DataJob> dataJobs = ConcurrentHashMap.newKeySet();
    private final Object instrumentLock = new Object();
    private final Map<Instrument, Integer> historyUsers = new HashMap<>();
    private final Set<Instrument> requestedInstruments = new HashSet<>();
    private final long historyTimeoutMillis;
    private final long historyRetryMillis;
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
                        if (sub.seedError != null) health(sub.id, "reconciling", sub.seedError, tick.getTime());
                        else if (sub.loading) health(sub.id, "reconciling", loadingReason(sub), tick.getTime());
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
        this(factory, output, historyTimeoutMillis, nowMillis, 2_000);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis, long historyRetryMillis) {
        this.factory = factory; this.output = output;
        this.historyTimeoutMillis = historyTimeoutMillis; this.nowMillis = nowMillis; this.historyRetryMillis = historyRetryMillis;
        deadlines.setRemoveOnCancelPolicy(true);
        reconnect.scheduleWithFixedDelay(this::heartbeat, 15, 15, TimeUnit.SECONDS);
    }
    private void heartbeat() {
        if (closing || client == null || !client.isConnected() || context == null) return;
        for (Subscription sub : subscriptions.values()) {
            try {
                if (sub.seedError != null) health(sub.id, "reconciling", sub.seedError, 0);
                else if (sub.loading) health(sub.id, "reconciling", loadingReason(sub), 0);
                else if (context.getDataService().isOfflineTime(nowMillis.getAsLong(), sub.instrument))
                    health(sub.id, "marketClosed", "JForex reports this market is closed", 0);
                else health(sub.id, "live", "Connected to JForex Bid feed", 0);
            } catch (Exception ignored) { health(sub.id, "live", "Connected to JForex Bid feed", 0); }
        }
    }

    // Only control work uses the command queue. A slow SDK HTTP history load
    // must never queue login checks, subscriptions or unsubscribe behind it.
    void dispatch(JsonObject request, Consumer<JsonObject> reply) {
        if (request.has("op") && request.get("op").getAsString().startsWith("trade")) {
            try { trading.execute(() -> reply.accept(handle(request))); }
            catch (RejectedExecutionException error) { reply.accept(failure(request.has("id") ? request.get("id").getAsString() : "", "restricted", "JForex trading is busy; no order was submitted by this request")); }
            return;
        }
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
                case "version" -> Map.of("protocol", 1, "sdkVersion", "3.6.51", "javaVersion", Runtime.version().feature(),
                    "instrumentCatalog", "all-available-v1", "trading", "market-bracket-v1");
                case "instruments" -> instruments();
                case "tradePreview" -> tradePreview(request);
                case "tradeSubmit" -> tradeSubmit(request);
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
        TreeSet<Instrument> instruments = new TreeSet<>(Comparator.comparing(Instrument::name));
        instruments.addAll(client.getAvailableInstruments());
        available = Collections.unmodifiableSet(instruments);
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
        return instrument.getType() == IFinancialInstrument.Type.FOREX && !isCrypto(instrument);
    }

    private static boolean isCrypto(Instrument instrument) {
        if (instrument.getGroup() != null) return instrument.getGroup().isCrypto();
        return instrument.getType() == IFinancialInstrument.Type.FOREX
            && (CRYPTO_CURRENCIES.contains(instrument.getPrimaryJFCurrency().getCurrencyCode())
                || CRYPTO_CURRENCIES.contains(instrument.getSecondaryJFCurrency().getCurrencyCode()));
    }

    static String market(Instrument instrument) {
        // The SDK also uses FOREX for cryptocurrencies such as BTC/USD. Keep
        // existing fiat identities while exposing those contracts as CFDs.
        if (isFiatForex(instrument)) return "forex";
        return instrument.getType() == IFinancialInstrument.Type.METAL ? "metal" : "cfd";
    }

    private static String assetType(Instrument instrument) {
        if (instrument.getGroup() != null) {
            var group = instrument.getGroup();
            if (group.isCrypto()) return "crypto";
            if (group.isCfdCommodities()) return "commodity";
            if (group.isCfdIndices()) return "index";
            if (group.isCfdStocks()) return "stock";
            if (group.isCfdETF()) return "etf";
            if (group.isCfdBonds()) return "bond";
        }
        if (isCrypto(instrument)) return "crypto";
        return market(instrument);
    }

    private Object instruments() {
        requireConnected();
        List<Object> result = new ArrayList<>();
        for (Instrument instrument : available) {
            Map<String, String> metadata = new HashMap<>();
            metadata.put("priceSide", "bid");
            metadata.put("volumeUnit", isFiatForex(instrument) ? "millions" : "provider units");
            metadata.put("sdkSymbol", instrument.toString());
            metadata.put("sdkType", instrument.getType().name());
            metadata.put("assetType", assetType(instrument));
            if (instrument.getDescription() != null) metadata.put("description", instrument.getDescription());
            if (instrument.getGroup() != null && instrument.getGroup().getName() != null)
                metadata.put("group", instrument.getGroup().getName());
            result.add(Map.of("nativeId", instrument.name(), "market", market(instrument),
                "baseAsset", instrument.getPrimaryJFCurrency().getCurrencyCode(), "quoteAsset", instrument.getSecondaryJFCurrency().getCurrencyCode(),
                "priceIncrement", BigDecimal.ONE.movePointLeft(instrument.getTickScale()).toPlainString(),
                "sizeIncrement", "", "availability", "available", "supportedIntervals", INTERVALS,
                "providerMetadata", metadata));
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
            long current = history.getBarStart(feed.period, nowMillis.getAsLong());
            List<IBar> source = new ArrayList<>();
            // A narrow missing suffix must not download a full page of old bars.
            // Keep count-based loading for larger pages so weekends do not
            // exhaust the requested number of actual historical candles.
            boolean currentRequested = separateCurrent(feed.period) && end >= current && from * 1000 <= current;
            long closedEnd = separateCurrent(feed.period) ? Math.min(end, current - feed.period.getInterval()) : end;
            if (from == 0 || from * 1000 <= closedEnd)
                source.addAll(readHistoryWindow(history, feed.instrument, feed.period, from * 1000, closedEnd, limit));
            while (currentRequested) {
                List<IBar> active = readRangeBars(history, feed.instrument, feed.period, current, current);
                long latest = history.getBarStart(feed.period, nowMillis.getAsLong());
                if (latest == current) { source.addAll(active); break; }
                // A snapshot completing across a boundary may predate the close.
                // Re-fetch only the requested newly closed range as native bars.
                long closedThrough = Math.min(end, latest - feed.period.getInterval());
                source.addAll(readHistoryWindow(history, feed.instrument, feed.period, current, closedThrough, limit));
                current = latest;
                currentRequested = end >= current;
            }
            if (!separateCurrent(feed.period)) current = history.getBarStart(feed.period, nowMillis.getAsLong());
            TreeMap<Long, JsonObject> result = new TreeMap<>();
            for (IBar bar : source) {
                if (inactiveFlat(feed.instrument, bar, current)) continue;
                if (bar.getTime() / 1000 < from || (to > 0 && bar.getTime() / 1000 >= to)) continue;
                result.put(bar.getTime(), candle(bar, bar.getTime() < current));
            }
            while (result.size() > limit) result.pollFirstEntry();
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
        synchronized (sub) {
            if (subscriptions.get(sub.id) != sub || closing) return;
            long generation = sub.seedGeneration.incrementAndGet();
            if (sub.seedRetry != null) { sub.seedRetry.cancel(false); sub.seedRetry = null; }
            if (sub.seed != null) sub.seed.cancel();
            sub.loading = true; sub.loadingCurrent = false; sub.seedError = null;
            sub.seed = submitData(() -> {
                try { seed(sub, generation); }
                catch (Exception ignored) { if (!Thread.currentThread().isInterrupted()) seedFailed(sub, generation, "JForex historical data could not be loaded."); }
            }, message -> seedFailed(sub, generation, message));
        }
    }
    private void seedFailed(Subscription sub, long generation, String message) {
        synchronized (sub) {
            if (subscriptions.get(sub.id) != sub || closing || !sub.seedGeneration.compareAndSet(generation, generation + 1)) return;
            sub.loading = true;
            // The request deadline also serves explicit history calls. Seed
            // failures own their retry and must never ask for a manual reload.
            String reason = message.contains("timed out") ? "JForex historical data loading timed out."
                : "JForex historical data could not be loaded.";
            long delay = Math.min(30_000L, historyRetryMillis * (1L << Math.min(sub.seedFailures++, 4)));
            sub.seedError = reason + " Retrying automatically; live quotes remain subscribed.";
            health(sub.id, "reconciling", sub.seedError, 0);
            scheduleSeedRetry(sub, generation + 1, delay);
        }
    }
    private void scheduleSeedRetry(Subscription sub, long generation, long delay) {
        sub.seedRetry = deadlines.schedule(() -> {
            synchronized (sub) {
                if (subscriptions.get(sub.id) != sub || closing || sub.seedGeneration.get() != generation) return;
                sub.seedRetry = null;
                if (client == null || !client.isConnected() || context == null) {
                    scheduleSeedRetry(sub, generation, delay);
                    return;
                }
                scheduleSeed(sub);
            }
        }, delay, TimeUnit.MILLISECONDS);
    }
    private void seed(Subscription sub, long generation) throws Exception {
        IHistory history = context.getHistory();
        long seedRevision = nextRevision();
        long current = history.getBarStart(sub.period, nowMillis.getAsLong());
        boolean separate = separateCurrent(sub.period);
        long end = separate ? current - sub.period.getInterval() : current;
        long from = sub.historyFrom;
        if (from == 0) {
            if (!publishSeed(sub, generation, seedRevision, readBars(history, sub.instrument, sub.period, separate ? 499 : 500, end), 0, end, !separate)) return;
        } else {
            // Account-scoped local candles already supply the old history. Fetch
            // only the overlap and missing suffix, bounded per SDK request/event.
            // Empty successful chunks are valid market closures.
            from = history.getBarStart(sub.period, from);
            while (from <= end) {
                if (closing || Thread.currentThread().isInterrupted() || sub.seedGeneration.get() != generation) throw new InterruptedException();
                long chunkEnd = Math.min(end, from + (SEED_RANGE_BARS - 1L) * sub.period.getInterval());
                List<IBar> source = readRangeBars(history, sub.instrument, sub.period, from, chunkEnd);
                if (!publishSeed(sub, generation, seedRevision, source, from, chunkEnd, chunkEnd == end && !separate)) return;
                from = chunkEnd + sub.period.getInterval();
            }
        }
        while (separate) {
            // JForex supplies a native in-progress snapshot. Keep that potentially
            // cold load independent of completed daily/weekly history publication
            // and its durable checkpoint; never replay ticks to backfill history.
            synchronized (sub) {
                if (subscriptions.get(sub.id) != sub || closing || Thread.currentThread().isInterrupted() || sub.seedGeneration.get() != generation) return;
                sub.loadingCurrent = true;
                health(sub.id, "reconciling", loadingReason(sub), 0);
            }
            List<IBar> active = readRangeBars(history, sub.instrument, sub.period, current, current);
            long latest = history.getBarStart(sub.period, nowMillis.getAsLong());
            if (latest > current) {
                // The snapshot may have been captured before the boundary. Read
                // the newly closed period again rather than certify stale OHLCV.
                long closedEnd = latest - sub.period.getInterval();
                List<IBar> closed = readRangeBars(history, sub.instrument, sub.period, current, closedEnd);
                if (!publishSeed(sub, generation, seedRevision, closed, current, closedEnd, false)) return;
                current = latest;
                continue;
            }
            publishSeed(sub, generation, seedRevision, active, current, current, true);
            return;
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
            if (requestedFrom == 0 && source.isEmpty()) {
                seedFailed(sub, generation, "JForex has not returned historical bars yet.");
                return false;
            }
            if (finished) { sub.loading = false; sub.seedError = null; sub.seedFailures = 0; }
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
                    health(sub.id, "marketClosed", "JForex reports this market is closed", 0);
                else health(sub.id, "live", "Connected to JForex Bid feed; waiting for market updates", 0);
            }
            return true;
        }
    }

    private List<IBar> readBars(IHistory history, Instrument instrument, Period period, int limit, long end) throws Exception {
        return loadBars(history, instrument, period, limit, 0, end, false);
    }
    private static boolean separateCurrent(Period period) {
        return period.equals(Period.DAILY) || period.equals(Period.WEEKLY);
    }
    private static String loadingReason(Subscription sub) {
        return sub.loadingCurrent ? "Historical JForex Bid candles loaded; refreshing current candle snapshot"
            : "Loading JForex Bid historical candles; live quotes remain subscribed";
    }
    private List<IBar> readHistoryWindow(IHistory history, Instrument instrument, Period period, long from, long end, int limit) throws Exception {
        long start = history.getBarStart(period, from);
        if (from > 0 && end >= start && (end - start) / period.getInterval() < limit)
            return loadBars(history, instrument, period, limit, start, end, true);
        return readBars(history, instrument, period, limit, end);
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
        if (sub != null) synchronized (sub) {
            sub.seedGeneration.incrementAndGet();
            if (sub.seedRetry != null) sub.seedRetry.cancel(false);
            if (sub.seed != null) sub.seed.cancel();
        }
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
    private final Set<String> tradeClaims = ConcurrentHashMap.newKeySet();

    private Instrument tradeInstrument(JsonObject request) {
        String symbol = text(request, "instrument", 128);
        for (Instrument item : available) if (item.name().equals(symbol)) return item;
        throw new Failure("unknownInstrument", "This instrument is unavailable for the connected JForex account");
    }
    private static double tradeNumber(JsonObject request, String field, boolean zero) {
        double value;
        try { value = request.get(field).getAsDouble(); }
        catch (Exception error) { throw new Failure("restricted", "Enter a valid " + field); }
        if (!Double.isFinite(value) || value < 0 || (!zero && value == 0)) throw new Failure("restricted", "Enter a valid " + field);
        return value;
    }
    private static boolean multiple(double value, double step) {
        if (!Double.isFinite(step) || step <= 0) return false;
        BigDecimal remainder = BigDecimal.valueOf(value).remainder(BigDecimal.valueOf(step)).abs();
        return remainder.compareTo(BigDecimal.valueOf(step).multiply(new BigDecimal("0.00000001"))) <= 0;
    }
    private Map<String, Object> tradePreview(JsonObject request) throws Exception {
        requireConnected();
        Instrument instrument = tradeInstrument(request);
        String side = text(request, "side", 4);
        if (!side.equals("buy") && !side.equals("sell")) throw new Failure("restricted", "Select Buy or Sell");
        double quantity = tradeNumber(request, "quantity", false), sl = tradeNumber(request, "stopLoss", true), tp = tradeNumber(request, "takeProfit", true);
        double contract = instrument.getAmountPerContract(), minimum = instrument.getMinTradeAmount() * contract,
            increment = instrument.getTradeAmountIncrement() * contract, maximum = instrument.getMaxTradeAmount() * contract;
        if (!Double.isFinite(contract) || contract <= 0 || !Double.isFinite(minimum) || minimum <= 0 || !Double.isFinite(increment) || increment <= 0)
            throw new Failure("restricted", "JForex did not supply verified order size rules for this instrument");
        if (quantity < minimum || !multiple(quantity, increment) || Double.isFinite(maximum) && maximum > 0 && quantity > maximum)
            throw new Failure("restricted", "Quantity must be at least " + minimum + " base units, in increments of " + increment + ", within the instrument maximum");
        double priceStep = Math.pow(10, -instrument.getTickScale());
        if ((sl > 0 && !multiple(sl, priceStep)) || (tp > 0 && !multiple(tp, priceStep)))
            throw new Failure("restricted", "SL and TP must respect the instrument price increment");
        ensureInstrument(instrument);
        ITick quote = context.getHistory().getLastTick(instrument);
        if (quote == null || nowMillis.getAsLong() - quote.getTime() > 30_000 || quote.getTime() > nowMillis.getAsLong() + 5_000)
            throw new Failure("restricted", "A fresh JForex Bid/Ask quote is required; wait for the market to open");
        double entry = side.equals("buy") ? quote.getAsk() : quote.getBid();
        if (!Double.isFinite(entry) || entry <= 0) throw new Failure("restricted", "The current JForex market price is unavailable");
        boolean buy = side.equals("buy");
        if ((sl > 0 && (buy ? sl >= entry : sl <= entry)) || (tp > 0 && (buy ? tp <= entry : tp >= entry)))
            throw new Failure("restricted", "SL and TP must be on the appropriate side of the current entry price");
        double leverage = context.getAccount().getLeverage(), requested = tradeNumber(request, "leverage", true);
        if (!Double.isFinite(leverage) || leverage <= 0) throw new Failure("restricted", "JForex account leverage is unavailable");
        if (requested != 0 && requested != leverage) throw new Failure("restricted", "Dukascopy leverage is fixed by the account and cannot be set per order");
        Map<String, Object> result = new HashMap<>();
        result.put("instrument", instrument.name()); result.put("side", side); result.put("quantity", quantity);
        result.put("stopLoss", sl); result.put("takeProfit", tp); result.put("entryPrice", entry);
        result.put("accountLeverage", leverage); result.put("minQuantity", minimum); result.put("quantityStep", increment); result.put("priceStep", priceStep);
        result.put("message", "Market entry uses the execution-time quote. Account leverage is fixed by Dukascopy; quantity is base units, not lots.");
        return result;
    }
    private Map<String, Object> tradeSubmit(JsonObject request) throws Exception {
        requireConnected();
        String intent = text(request, "intent", 32);
        if (!intent.matches("[a-f0-9]{32}")) throw new Failure("restricted", "Invalid trade confirmation");
        // Never submit a confirmation twice, including when an earlier reply timed out.
        if (!tradeClaims.add(intent)) throw new Failure("restricted", "This trade confirmation was already used; inspect the account before submitting another trade");
        Map<String, Object> preview = tradePreview(request);
        Instrument instrument = tradeInstrument(request);
        String side = (String) preview.get("side");
        double quantity = (Double) preview.get("quantity"), sl = (Double) preview.get("stopLoss"), tp = (Double) preview.get("takeProfit");
        IContext active = context;
        Future<IOrder> submission = active.executeTask(() -> {
            if (closing || context != active || client == null || !client.isConnected()) throw new Failure("offline", "JForex disconnected before order submission");
            tradePreview(request); // revalidate fresh price/levels on the SDK strategy thread
            return active.getEngine().submitOrder("CD_" + intent, instrument, side.equals("buy") ? IEngine.OrderCommand.BUY : IEngine.OrderCommand.SELL,
                quantity / 1_000_000.0, 0, 5, sl, tp);
        });
        IOrder order;
        try { order = submission.get(10, TimeUnit.SECONDS); }
        catch (Exception error) {
            submission.cancel(false);
            return Map.of("status", "unknown", "message", "JForex submission outcome is unknown. Inspect account orders labelled CD_" + intent + " before another trade; no automatic retry was made.", "protectionConfirmed", false);
        }
        if (order == null) return Map.of("status", "unknown", "message", "JForex returned no order; inspect your account before another trade.", "protectionConfirmed", false);
        long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(Math.min(10_000, historyTimeoutMillis));
        while (System.nanoTime() < deadline && !closing) {
            IOrder.State state = order.getState();
            boolean protectedOrder = (sl == 0 || order.getStopLossPrice() == sl) && (tp == 0 || order.getTakeProfitPrice() == tp);
            if (state == IOrder.State.FILLED && protectedOrder || state == IOrder.State.CANCELED || state == IOrder.State.CLOSED) break;
            Thread.sleep(20);
        }
        IOrder.State state = order.getState();
        boolean protection = (sl == 0 || order.getStopLossPrice() == sl) && (tp == 0 || order.getTakeProfitPrice() == tp);
        Map<String, Object> result = new HashMap<>();
        boolean fullyFilled = state == IOrder.State.FILLED && Math.abs(order.getAmount() * 1_000_000.0 - quantity) <= Math.max(0.00000001, quantity * 0.000000001);
        result.put("status", state == IOrder.State.FILLED ? fullyFilled && protection ? "filled" : "partial" : state == IOrder.State.CANCELED ? "rejected" : "submitted");
        result.put("orderID", order.getId() == null ? "" : order.getId());
        result.put("filledQuantity", state == IOrder.State.FILLED ? order.getAmount() * 1_000_000.0 : 0);
        result.put("entryPrice", order.getOpenPrice()); result.put("protectionConfirmed", protection && state == IOrder.State.FILLED);
        result.put("message", fullyFilled && protection ? "JForex reports the market order filled with the requested protective levels." :
            "JForex order state: " + state + ". Inspect the account; fill and protective levels are not fully confirmed. No automatic retry was made.");
        return result;
    }

    private Feed feed(JsonObject value) {
        if (value == null || !"dukascopy".equals(text(value, "exchange", 32)))
            throw new Failure("unknownInstrument", "Select a Dukascopy instrument");
        String symbol = text(value, "instrument", 128), timeframe = text(value, "timeframe", 8);
        Instrument instrument = null;
        for (Instrument item : available) if (item.name().equals(symbol)) { instrument = item; break; }
        if (instrument == null || !market(instrument).equals(text(value, "market", 32)))
            throw new Failure("unknownInstrument", "This instrument or market is unavailable for the connected JForex account");
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
        closing = true;
        for (Subscription sub : subscriptions.values()) unsubscribe(sub.id);
        available = Set.of(); context = null; connectedOnce = false;
        for (DataJob job : dataJobs) job.cancel();
        synchronized (instrumentLock) { historyUsers.clear(); requestedInstruments.clear(); }
        IClient old = client; client = null;
        if (old != null) { try { if (strategyId >= 0) old.stopStrategy(strategyId); } catch (Exception ignored) { } try { old.disconnect(); } catch (Exception ignored) { } }
        strategyId = -1;
    }
    public void close() { disconnect(); reconnect.shutdownNow(); deadlines.shutdownNow(); data.shutdownNow(); trading.shutdownNow(); }
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
        final AtomicLong seedGeneration = new AtomicLong(); volatile DataJob seed; volatile boolean loading; volatile boolean loadingCurrent; volatile String seedError; volatile long historyFrom;
        Future<?> seedRetry; int seedFailures;
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

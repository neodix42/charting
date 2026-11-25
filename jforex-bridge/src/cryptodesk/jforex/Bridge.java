package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.instrument.IFinancialInstrument;
import com.dukascopy.api.instrument.IMarketInfo;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.io.*;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.time.format.DateTimeFormatter;
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
    static final long TICK_HOUR = 3_600_000;
    static final int TICK_EXPORT_MAX_ROWS = 1_000_000;
    static final long TICK_EXPORT_MAX_BYTES = 64L * 1024 * 1024;
    static final long TICK_HISTORY_TIMEOUT_MILLIS = 120_000;
    static final String NATIVE_DOWNLOAD_TIMEOUT = "JForex native daily download timed out. Completed history is retained; retry to resume.";
    // Read-only planning may use an actual last quote over a market closure.
    // Execution and maximum-size calculations retain their 30-second limit.
    static final long REFERENCE_QUOTE_MAX_AGE = 14L * 86_400_000;
    private static final DateTimeFormatter MARKET_HOURS_TIME = DateTimeFormatter.ofPattern("HH:mm:ss", Locale.ROOT);
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
    private final AtomicLong hourlyFileRecoveries = new AtomicLong();
    private final AtomicBoolean reconnecting = new AtomicBoolean();
    private final ScheduledExecutorService reconnect = Executors.newSingleThreadScheduledExecutor(r -> daemon(r, "jforex-reconnect"));
    private final ScheduledThreadPoolExecutor deadlines = new ScheduledThreadPoolExecutor(1, r -> daemon(r, "jforex-deadlines"));
    private final ExecutorService data = new ThreadPoolExecutor(2, 2, 0, TimeUnit.MILLISECONDS,
        new ArrayBlockingQueue<>(32), r -> daemon(r, "jforex-history"));
    private final ExecutorService trading = new ThreadPoolExecutor(1, 1, 0, TimeUnit.MILLISECONDS, new ArrayBlockingQueue<>(8), r -> daemon(r, "jforex-trading"));
    private final Set<DataJob> dataJobs = ConcurrentHashMap.newKeySet();
    private final Object tickExportLock = new Object();
    private final Map<String, TickExport> tickExports = new HashMap<>();
    private final Set<String> canceledTickExports = new HashSet<>();
    private final LinkedHashSet<String> finishedTickExports = new LinkedHashSet<>();
    private final Object instrumentLock = new Object();
    private final Map<Instrument, Integer> historyUsers = new HashMap<>();
    private final Set<Instrument> requestedInstruments = new HashSet<>();
    private final long historyTimeoutMillis;
    private final long tickHistoryTimeoutMillis;
    private final ThreadLocal<Boolean> nativeHistoryDownload = ThreadLocal.withInitial(() -> false);
    private final ThreadLocal<Long> nativeDownloadDeadline = new ThreadLocal<>();
    private static final String HOURLY_DOWNLOAD_TIMEOUT = "JForex hourly history download timed out. Completed history is retained; retry to resume.";
    private final long historyRetryMillis;
    private final LongSupplier nowMillis;
    private final Supplier<com.dukascopy.charts.data.datacache.IFeedDataProvider> defaultFeedProvider;
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
                                || start == sub.lastTime && tick.getTime() < sub.quoteFloor
                                || sub.last != null && start == sub.lastTime && sub.last.get("closed").getAsBoolean()) continue;
                        if (start > sub.lastTime) sub.quoteFloor = 0;
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
        this(factory, output, 45_000, System::currentTimeMillis, 2_000, TICK_HISTORY_TIMEOUT_MILLIS);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis) {
        this(factory, output, historyTimeoutMillis, System::currentTimeMillis);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis) {
        this(factory, output, historyTimeoutMillis, nowMillis, 2_000);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis, long historyRetryMillis) {
        this(factory, output, historyTimeoutMillis, nowMillis, historyRetryMillis, historyTimeoutMillis);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis, long historyRetryMillis, long tickHistoryTimeoutMillis) {
        this(factory, output, historyTimeoutMillis, nowMillis, historyRetryMillis, tickHistoryTimeoutMillis, com.dukascopy.charts.data.datacache.FeedDataProvider::getDefaultInstance);
    }
    Bridge(Supplier<IClient> factory, Consumer<JsonObject> output, long historyTimeoutMillis, LongSupplier nowMillis, long historyRetryMillis, long tickHistoryTimeoutMillis, Supplier<com.dukascopy.charts.data.datacache.IFeedDataProvider> defaultFeedProvider) {
        this.factory = factory; this.output = output;
        this.historyTimeoutMillis = historyTimeoutMillis; this.tickHistoryTimeoutMillis = tickHistoryTimeoutMillis;
        this.nowMillis = nowMillis; this.historyRetryMillis = historyRetryMillis;
        this.defaultFeedProvider = defaultFeedProvider;
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
        if (request.has("op") && "historyTicks".equals(request.get("op").getAsString())) {
            dispatchTickExport(request, reply); return;
        }
        if (request.has("op") && "historyArchiveDiagnostic".equals(request.get("op").getAsString())) {
            String id = request.has("id") ? request.get("id").getAsString() : "";
            AtomicBoolean replied = new AtomicBoolean();
            Consumer<JsonObject> once = response -> { if (replied.compareAndSet(false,true)) reply.accept(response); };
            submitData(() -> {
                nativeDownloadDeadline.set(System.nanoTime()+TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis));
                try { once.accept(handle(request)); } finally { nativeDownloadDeadline.remove(); }
            }, message -> once.accept(failure(id,"timeout",message)),tickHistoryTimeoutMillis,false,HOURLY_DOWNLOAD_TIMEOUT);
            return;
        }
        if (request.has("op") && Set.of("historyCalendar", "historyCandleDiagnostic").contains(request.get("op").getAsString())) {
            String id = request.has("id") ? request.get("id").getAsString() : "";
            submitData(() -> reply.accept(handle(request)), message -> reply.accept(failure(id, "timeout", message))); return;
        }
        if (request.has("op") && request.get("op").getAsString().startsWith("trade")) {
            try { trading.execute(() -> reply.accept(handle(request))); }
            catch (RejectedExecutionException error) { reply.accept(failure(request.has("id") ? request.get("id").getAsString() : "", "restricted", "JForex trading is busy; no order was submitted by this request")); }
            return;
        }
        if (request.has("op") && "history".equals(request.get("op").getAsString())) {
            String id = request.has("id") ? request.get("id").getAsString() : "";
            final boolean download;
            try { download = nativeHistoryDownload(request); }
            catch (Failure error) { reply.accept(failure(id, error.kind, error.getMessage())); return; }
            AtomicBoolean replied = new AtomicBoolean();
            Consumer<JsonObject> once = response -> { if (replied.compareAndSet(false, true)) reply.accept(response); };
            Runnable work = () -> {
                try {
                    nativeHistoryDownload.set(download);
                    if (download) nativeDownloadDeadline.set(System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis));
                    once.accept(handle(request));
                } finally { nativeHistoryDownload.remove(); nativeDownloadDeadline.remove(); }
            };
            Consumer<String> failed = message -> once.accept(failure(id, "timeout", message));
            if (download) submitData(work, failed, tickHistoryTimeoutMillis, false, request.has("fullOpenHours") ? HOURLY_DOWNLOAD_TIMEOUT : NATIVE_DOWNLOAD_TIMEOUT);
            else submitData(work, failed);
        } else reply.accept(handle(request));
    }

    private boolean nativeHistoryDownload(JsonObject request) {
        if (!request.has("download")) return false;
        if (!request.get("download").isJsonPrimitive() || !request.getAsJsonPrimitive("download").isBoolean())
            throw new Failure("internal", "Invalid JForex native history download flag");
        if (!request.get("download").getAsBoolean()) return false;
        JsonObject key = request.has("feedKey") && request.get("feedKey").isJsonObject() ? request.getAsJsonObject("feedKey") : new JsonObject();
        String instrument = text(key, "instrument", 128), market = text(key, "market", 32);
        long from = optionalLong(request, "from"), to = optionalLong(request, "to");
        long day = 86_400;
        boolean hourly = text(key, "timeframe", 8).equals("1h"), fullOpenHours = false;
        if (request.has("fullOpenHours")) {
            if (!request.get("fullOpenHours").isJsonPrimitive() || !request.getAsJsonPrimitive("fullOpenHours").isBoolean())
                throw new Failure("internal", "Invalid verified hourly history flag");
            fullOpenHours = request.get("fullOpenHours").getAsBoolean();
        }
        long bucket = hourly ? 3_600 : day, maximum = hourly ? 1000 * bucket : 366 * day;
        boolean nativeInstrument = !instrument.isEmpty() && Set.of("forex", "metal", "cfd").contains(market);
        if (!nativeInstrument || hourly != request.has("fullOpenHours") || hourly && !fullOpenHours && !(instrument.equals("BTCUSD") && market.equals("cfd"))
                || !text(key, "exchange", 32).equals("dukascopy") || !text(key, "timeframe", 8).equals(hourly ? "1h" : "1d")
                || from < 0 || to <= from || from % bucket != 0 || to % bucket != 0 || to - from > maximum
                || to > Math.floorDiv(nowMillis.getAsLong(), bucket * 1000) * bucket)
            throw new Failure("internal", "Native JForex downloads require at most 366 closed UTC days or 1000 verified closed UTC hours");
        integer(request, "limit", 1, 1000);
        return true;
    }

    private void dispatchTickExport(JsonObject request, Consumer<JsonObject> reply) {
        String id = request.has("id") ? request.get("id").getAsString() : "";
        final String token;
        final TickExport export = new TickExport();
        try {
            token = text(request, "exportId", 128);
            synchronized (tickExportLock) {
                // A cancellation may reach the pipe before a canceled Go
                // caller's original write. Retain that cancellation until the
                // corresponding request arrives; never reopen its output.
                if (canceledTickExports.remove(token)) {
                    rememberFinishedTickExport(token);
                    throw new Failure("timeout", "JForex tick history export was canceled.");
                }
                if (tickExports.containsKey(token) || finishedTickExports.contains(token))
                    throw new Failure("internal", "Duplicate JForex tick history export");
                if (tickExports.size() >= 32) throw new Failure("restricted", "JForex tick history export queue is full");
                tickExports.put(token, export);
            }
        } catch (Failure error) { reply.accept(failure(id, error.kind, error.getMessage())); return; }
        AtomicBoolean replied = new AtomicBoolean();
        Consumer<JsonObject> once = response -> { if (replied.compareAndSet(false, true)) reply.accept(response); };
        export.job = submitData(() -> {
            try { once.accept(handle(request)); }
            finally { completeTickExport(token, export); }
        }, message -> {
            export.cancel(); completeTickExport(token, export);
            once.accept(failure(id, "timeout", message));
        }, tickHistoryTimeoutMillis, false, "JForex tick history loading timed out. Completed quote hours are retained; retry to resume.");
        if (export.canceled.get()) export.job.cancel();
    }

    // Called with tickExportLock held. Completed tokens are bounded and prevent
    // a late cancellation from creating an orphaned pre-start cancellation.
    private void rememberFinishedTickExport(String token) {
        finishedTickExports.add(token);
        while (finishedTickExports.size() > 128) finishedTickExports.remove(finishedTickExports.iterator().next());
    }
    private void completeTickExport(String token, TickExport export) {
        synchronized (tickExportLock) {
            tickExports.remove(token, export); canceledTickExports.remove(token); rememberFinishedTickExport(token);
        }
    }
    private Object cancelTickExport(String token) {
        TickExport export;
        synchronized (tickExportLock) {
            export = tickExports.get(token);
            if (export == null && !finishedTickExports.contains(token)) {
                if (canceledTickExports.size() >= 64 && !canceledTickExports.contains(token))
                    throw new Failure("restricted", "Too many unacknowledged JForex tick history cancellations");
                canceledTickExports.add(token);
            }
        }
        if (export != null) {
            // Closing the file precedes this control reply, even if a provider
            // getTicks call ignores interruption and returns after cancellation.
            export.cancel();
            if (export.job != null) export.job.cancel();
        }
        return Map.of("cancelled", true);
    }
    private final class TickExport {
        final AtomicBoolean canceled = new AtomicBoolean();
        volatile DataJob job;
        private FileChannel file;
        synchronized void attach(FileChannel value) throws Exception {
            if (canceled.get() || closing || Thread.currentThread().isInterrupted()) { value.close(); throw new InterruptedException(); }
            file = value;
        }
        synchronized void cancel() {
            canceled.set(true);
            if (file != null) try { file.close(); } catch (IOException ignored) { }
        }
        void check() throws InterruptedException {
            if (canceled.get() || closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
        }
    }

    private DataJob submitData(Runnable action, Consumer<String> failed) {
        return submitData(action, failed, historyTimeoutMillis, true,
            "JForex historical data loading timed out. Live quotes can continue; retry the chart to reload history.");
    }
    private DataJob submitData(Runnable action, Consumer<String> failed, long timeoutMillis, boolean includeQueue, String timeoutMessage) {
        DataJob job = new DataJob(failed);
        dataJobs.add(job);
        job.task = new FutureTask<>(() -> {
            try {
                if (!job.finished.get()) {
                    // A download may wait behind chart history or another SDK
                    // cache read. Its tick budget measures actual work rather
                    // than expiring before getTicks has even been attempted.
                    if (!includeQueue) job.deadline(timeoutMillis, timeoutMessage);
                    if (!job.finished.get()) action.run();
                }
            }
            finally { job.finish(); }
            return null;
        });
        if (includeQueue) job.deadline(timeoutMillis, timeoutMessage);
        try { data.execute(job.task); }
        catch (RejectedExecutionException error) { job.fail("JForex history request queue is full. Retry after the pending requests finish."); }
        return job;
    }

    private final class DataJob {
        final AtomicBoolean finished = new AtomicBoolean(); final Consumer<String> failed;
        volatile FutureTask<Void> task; volatile Future<?> timeout;
        DataJob(Consumer<String> failed) { this.failed = failed; }
        void deadline(long millis, String message) {
            Future<?> scheduled = deadlines.schedule(() -> fail(message), millis, TimeUnit.MILLISECONDS);
            timeout = scheduled;
            if (finished.get()) scheduled.cancel(false);
        }
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
                case "version" -> {
                    Map<String, Object> version = new HashMap<>(Map.of("protocol", 1, "sdkVersion", "3.6.51", "javaVersion", Runtime.version().feature(),
                        "instrumentCatalog", "all-available-v1", "trading", "market-bracket-v1", "tradeEstimates", "account-currency-pnl-v1", "tradeMetrics", "account-metrics-v1", "tradeHistory", "platform-orders-v1", "tradePnL", "account-pnl-v1", "tickHistory", "bid-hour-file-v1"));
                    version.put("hourlyHistory", "bid-full-open-hour-v2"); version.put("nativeHourlyHistory", "bid-native-hour-v2"); version.put("historicalCalendar", "read-calendar-proof-v1"); yield version;
                }
                case "instruments" -> instruments();
                case "historyCalendar" -> historyCalendar(request);
                case "historyCandleDiagnostic" -> historyCandleDiagnostic(request);
                case "historyArchiveDiagnostic" -> historyArchiveDiagnostic(request);
                case "tradePreview" -> tradePreview(request);
                case "tradeEstimate" -> tradeEstimate(request);
                case "tradeMetrics" -> tradeMetrics(request);
                case "tradeSubmit" -> tradeSubmit(request);
                case "tradeHistory" -> tradeHistory(request);
                case "tradeClose" -> tradeClose(request);
                case "history" -> history(request);
                case "historyTicks" -> historyTicks(request);
                case "historyTicksCancel" -> cancelTickExport(text(request, "exportId", 128));
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

    private Object instruments() throws InterruptedException {
        requireConnected();
        awaitMarketHours();
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
            metadata.putAll(instrumentHoursMetadata(instrument));
            result.add(Map.of("nativeId", instrument.name(), "market", market(instrument),
                "baseAsset", instrument.getPrimaryJFCurrency().getCurrencyCode(), "quoteAsset", instrument.getSecondaryJFCurrency().getCurrencyCode(),
                "priceIncrement", BigDecimal.ONE.movePointLeft(instrument.getTickScale()).toPlainString(),
                "sizeIncrement", "", "availability", "available", "supportedIntervals", INTERVALS,
                "providerMetadata", metadata));
        }
        return result;
    }

    private void awaitMarketHours() throws InterruptedException {
        // Authentication supplies instrument identities before the asynchronous
        // market/calendar responses create and fill their shared records. Null
        // is also pending; one ready instrument says nothing about its peers.
        // Share one deadline across the catalog, never one wait per instrument.
        long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(Math.min(2_000, historyTimeoutMillis));
        Set<Instrument> pending = new HashSet<>(available);
        while (!closing && !pending.isEmpty() && System.nanoTime() < deadline) {
            for (Iterator<Instrument> iterator = pending.iterator(); iterator.hasNext();) {
                if (closing || System.nanoTime() >= deadline) return;
                Map<String, String> hours = instrumentHoursMetadata(iterator.next());
                if ("available".equals(hours.get("marketHoursStatus")) && !needsCalendar(hours)) iterator.remove();
            }
            long remaining = deadline - System.nanoTime();
            if (!pending.isEmpty() && remaining > 0) TimeUnit.NANOSECONDS.sleep(Math.min(TimeUnit.MILLISECONDS.toNanos(20), remaining));
        }
    }

    private Map<String, String> instrumentHoursMetadata(Instrument instrument) {
        Map<String, String> hours = marketHoursMetadata(instrument);
        boolean unavailable = !"available".equals(hours.get("marketHoursStatus"));
        // A daily window cannot establish continuous, overnight or wrapped-week
        // sessions. Prefer actual dated closures while retaining supplied regular
        // hours when the calendar has not loaded within the shared deadline.
        if (unavailable || needsCalendar(hours)) {
            Map<String, String> calendar = calendarHoursMetadata(instrument);
            if (unavailable || "available".equals(calendar.get("marketHoursStatus"))) hours = calendar;
        }
        return hours;
    }

    private static boolean needsCalendar(Map<String, String> hours) {
        return "JForex regular market session".equals(hours.get("marketHoursSource"))
            && (Integer.parseInt(hours.get("marketHoursFirstDay")) > Integer.parseInt(hours.get("marketHoursLastDay"))
                || hours.get("marketHoursOpen").compareTo(hours.get("marketHoursClose")) >= 0);
    }

    private Map<String, String> calendarHoursMetadata(Instrument instrument) {
        try {
            IContext current = context;
            IDataService service = current == null ? null : current.getDataService();
            if (service == null) return Map.of("marketHoursStatus", "unavailable");
            long day = 86_400_000L, midnight = Math.floorDiv(nowMillis.getAsLong(), day) * day;
            long from = midnight - 8 * day, to = midnight + 8 * day;
            Set<ITimeDomain> domains = service.getOfflineTimeDomains(from, to, instrument);
            // The public API returns an empty set for both an unloaded calendar
            // and no closures. Do not turn either case into invented 24/7 hours.
            if (domains == null || domains.isEmpty() || domains.size() > 256) return Map.of("marketHoursStatus", "unavailable");
            List<long[]> intervals = new ArrayList<>();
            for (ITimeDomain domain : domains) {
                if (domain == null || domain.getStart() <= 0 || domain.getEnd() <= domain.getStart()) return Map.of("marketHoursStatus", "unavailable");
                long start = Math.max(from, domain.getStart()), end = Math.min(to, domain.getEnd());
                if (start < end) intervals.add(new long[]{start, end});
            }
            if (intervals.isEmpty()) return Map.of("marketHoursStatus", "unavailable");
            intervals.sort(Comparator.comparingLong(value -> value[0]));
            List<long[]> merged = new ArrayList<>();
            for (long[] interval : intervals) {
                if (merged.isEmpty() || merged.get(merged.size() - 1)[1] < interval[0]) merged.add(interval);
                else merged.get(merged.size() - 1)[1] = Math.max(merged.get(merged.size() - 1)[1], interval[1]);
            }
            // Pinned JForexDataService converts inclusive holiday endpoints to
            // exclusive ITimeDomain ends (+1ms). Preserve those exact bounds.
            return Map.of("marketHoursStatus", "available", "marketHoursSource", "JForex instrument calendar",
                "marketHoursTimeZone", "UTC", "marketHoursFrom", Long.toString(from), "marketHoursTo", Long.toString(to),
                "marketHoursClosedIntervals", JSON.toJson(merged));
        } catch (RuntimeException ignored) {
            return Map.of("marketHoursStatus", "unavailable");
        }
    }

    private static Map<String, String> marketHoursMetadata(Instrument instrument) {
        try {
            IMarketInfo info = instrument.getMarketInfo();
            if (info != null) {
                var zone = info.getZoneId();
                var firstDay = info.getFirstWorkingDay();
                var lastDay = info.getLastWorkingDay();
                var open = info.getOpenTime();
                var close = info.getCloseTime();
                if (zone != null && firstDay != null && lastDay != null && open != null && close != null) {
                    // This is the SDK's regular market-session envelope, not
                    // instrument tradability: it cannot describe intraday
                    // breaks, holidays or trading halts. Keep the market zone
                    // so daylight-saving changes do not become fixed UTC hours.
                    return Map.of("marketHoursStatus", "available",
                        "marketHoursSource", "JForex regular market session",
                        "marketHoursTimeZone", zone.getId(),
                        "marketHoursFirstDay", Integer.toString(firstDay.getValue()),
                        "marketHoursLastDay", Integer.toString(lastDay.getValue()),
                        "marketHoursOpen", open.format(MARKET_HOURS_TIME),
                        "marketHoursClose", close.format(MARKET_HOURS_TIME));
                }
            }
        } catch (RuntimeException ignored) {
            // One unavailable SDK market record must not hide the catalog.
        }
        return Map.of("marketHoursStatus", "unavailable");
    }

    private List<JsonObject> history(JsonObject request) throws Exception {
        requireConnected();
        nativeHistoryDownload(request);
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
            if (nativeHistoryDownload.get())
                source.addAll(readNativeDownloadBars(history, feed.instrument, feed.period, from * 1000, closedEnd, request.has("fullOpenHours") && request.get("fullOpenHours").getAsBoolean()));
            else if (from == 0 || from * 1000 <= closedEnd)
                source.addAll(readHistoryWindow(history, feed.instrument, feed.period, from * 1000, closedEnd, limit));
            while (currentRequested) {
                List<IBar> active = readCurrentBars(history, feed.instrument, feed.period, current);
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

    private List<IBar> readNativeDownloadBars(IHistory history, Instrument instrument, Period period, long from, long end, boolean fullOpenHours) throws Exception {
        IContext activeContext = context;
        long readyBy = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis);
        while (activeContext != null && !activeContext.getSubscribedInstruments().contains(instrument)) {
            if (closing || Thread.currentThread().isInterrupted() || activeContext != context) throw new InterruptedException();
            if (System.nanoTime() >= readyBy) throw new Failure("timeout", NATIVE_DOWNLOAD_TIMEOUT);
            Thread.sleep(10);
        }
        if (activeContext == null || closing || Thread.currentThread().isInterrupted() || activeContext != context) throw new InterruptedException();
        if (period == Period.ONE_HOUR) return readNativeHourlyDays(history, activeContext, instrument, from, end, fullOpenHours);
        // readBars streams callbacks from the SDK's background cache loader.
        // A download needs the blocking server/cache-loaded collection contract
        // of getBars. Only validated, <=366-day closed native D1 requests reach
        // this bounded worker; quote callbacks and chart reads remain async.
        List<IBar> bars = history.getBars(instrument, period, OfferSide.BID, Filter.WEEKENDS, from, end);
        if (closing || Thread.currentThread().isInterrupted() || activeContext != context) throw new InterruptedException();
        long duration = period.getInterval();
        if (bars == null || bars.size() > (period == Period.DAILY ? 366 : 1000) || bars.size() > (end - from) / duration + 1)
            throw new Failure("internal", "JForex native daily download exceeded its requested candle bounds");
        long previous = -1;
        for (IBar bar : bars) {
            if (bar == null) throw new Failure("internal", "JForex returned a missing native daily candle");
            validateBar(bar);
            if (bar.getTime() < from || bar.getTime() > end || bar.getTime() % duration != 0 || bar.getTime() <= previous)
                throw new Failure("internal", "JForex returned an invalid native daily candle time or ordering");
            previous = bar.getTime();
        }
        return bars;
    }

    private List<IBar> readNativeHourlyDays(IHistory history, IContext activeContext, Instrument instrument, long from, long end, boolean fullOpenHours) throws Exception {
        long hour = Period.ONE_HOUR.getInterval(), day = Period.DAILY.getInterval();
        boolean calendarPending = fullOpenHours && historicalCalendarPending(activeContext.getDataService(), instrument);
        List<long[]> closures = fullOpenHours && !calendarPending ? verifiedHistoricalClosures(activeContext.getDataService(), instrument, from, end + hour) : null;
        List<IBar> result = new ArrayList<>();
        boolean[] metadataReloaded = {false};
        Map<Long, VerifiedHourlyMonth> verifiedMonths = new HashMap<>();
        Long deadline = nativeDownloadDeadline.get();
        for (long cursor = from; cursor <= end;) {
            if (closing || Thread.currentThread().isInterrupted() || activeContext != context) throw new InterruptedException();
            if (deadline != null && System.nanoTime() >= deadline) throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT);
            if(!verifiedMonths.isEmpty())requireCachedHourlySource(verifiedMonths.values().iterator().next(),activeContext,instrument,deadline);
            long until = Math.min(end + hour, (Math.floorDiv(cursor, day) + 1) * day);
            boolean openHour = !fullOpenHours || calendarPending || hasFullOpenHour(closures, cursor, until, hour);
            if (openHour) {
                // The pinned SDK's multi-day getBars path can silently return
                // empty spans or carry a prior close across unloaded days.
                // Bound every SDK read to its UTC source day. Calendar proof
                // and the outer120s job budget cover the entire export.
                List<IBar> bars = history.getBars(instrument, Period.ONE_HOUR, OfferSide.BID, Filter.NO_FILTER, cursor, until - hour);
                if (deadline != null && System.nanoTime() >= deadline) throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT);
                if(!verifiedMonths.isEmpty())requireCachedHourlySource(verifiedMonths.values().iterator().next(),activeContext,instrument,deadline);
                if (calendarPending) {
                    // Subscription acknowledgement can precede SDK calendar
                    // initialization. A real day load may warm it, but no row
                    // is accepted until the full historical proof succeeds.
                    closures = verifiedHistoricalClosures(activeContext.getDataService(), instrument, from, end + hour); calendarPending = false;
                }
                if (bars == null || bars.size() > (until - cursor) / hour) throw new Failure("internal", "JForex hourly source exceeded its requested day");
                int observed = collectHourlyObservations(result, bars, closures, cursor, until, fullOpenHours);
                if (observed ==0) {
                    if (closures == null) closures = verifiedHistoricalClosures(activeContext.getDataService(), instrument, from, end + hour);
                    if (fullOpenHours ? hasFullOpenHour(closures, cursor, until, hour) : !entirelyClosed(closures, cursor, until)) {
                        // Calendar openness is not evidence that quotes exist.
                        // getBars can cache a successful false-empty response;
                        // the independent date-range loader bypasses that cache.
                        HourlyRecovery recovered;
                        try { recovered = recoverHourlyDay(history, activeContext, instrument, cursor, until, end + hour, metadataReloaded, verifiedMonths); }
                        catch (Failure failure) {
                            if (failure.kind.equals("timeout")) throw failure;
                            throw new Failure(failure.kind, failure.getMessage() + " (" + instrument.name() + " " + java.time.Instant.ofEpochMilli(cursor).toString().substring(0,10) + ")");
                        }
                        if (collectHourlyObservations(result, recovered.bars(), closures, cursor, until, fullOpenHours) ==0 && !recovered.verifiedAbsence())
                            throw new Failure("restricted", "JForex could not confirm observed hourly history for " + instrument.name() + " " + java.time.Instant.ofEpochMilli(cursor).toString().substring(0,10) + "; incomplete history was not cached");
                    }
                }
            }
            cursor = until;
        }
        return result;
    }

    private static int collectHourlyObservations(List<IBar> result, List<IBar> bars, List<long[]> closures, long from, long until, boolean fullOpenHours) {
        long hour = Period.ONE_HOUR.getInterval(), previous = -1; int observed =0;
        if (bars == null || bars.size() > (until - from) / hour) throw new Failure("internal", "JForex hourly source exceeded its requested day");
        for (IBar bar : bars) {
            if (bar == null) throw new Failure("internal", "JForex returned a missing hourly candle");
            validateBar(bar);
            if (bar.getTime() < from || bar.getTime() >= until || bar.getTime() % hour !=0 || bar.getTime() <= previous)
                throw new Failure("internal", "JForex returned invalid hourly source ordering");
            previous = bar.getTime();
            // HistoryData manufactures exactly this shape for missing
            // observations. Real zero-volume non-flat OHLC survives;
            // ambiguous zero-volume flats cannot establish history.
            if (bar.getVolume() ==0 && bar.getOpen() == bar.getClose() && bar.getHigh() == bar.getLow()) continue;
            if (fullOpenHours && intersectsClosure(closures, bar.getTime(), bar.getTime() + hour)) continue;
            result.add(bar); observed++;
        }
        return observed;
    }

    private void requireHourlySource(IContext active, IClient connection, String account, long deadline) throws InterruptedException {
        if (closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
        if (System.nanoTime() >= deadline) throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT);
        if (context != active || client != connection || connection == null || !connection.isConnected()
                || active.getAccount() == null || !Objects.equals(account, active.getAccount().getAccountId()))
            throw new Failure("offline", "JForex hourly history source changed or disconnected; incomplete history was not cached");
    }

    private static boolean knownFirstHourlyCandle(Long value, long now) {
        return value != null && value >0 && value < Long.MAX_VALUE && value <= now;
    }

    private record HourlyRecovery(List<IBar> bars, boolean verifiedAbsence) { }
    private record HourlyFileAttempt(com.dukascopy.charts.data.datacache.Data[] rows,boolean failed) { }
    private record VerifiedHourlyMonth(List<IBar> bars,com.dukascopy.charts.data.datacache.IFeedDataProvider provider,long first,IContext context,IClient client,String account) { }

    private void requireCachedHourlySource(VerifiedHourlyMonth proof,IContext active,Instrument instrument,Long deadline)throws Exception{
        requireHourlySource(active,proof.client(),proof.account(),deadline==null?Long.MAX_VALUE:deadline);
        if(active!=proof.context()||!(active.getDataService() instanceof com.dukascopy.api.impl.connect.JForexDataService service)
                ||service.getFeedDataProvider()!=proof.provider()||defaultFeedProvider.get()!=proof.provider()
                ||!Objects.equals(proof.first(),proof.provider().getFeedMetadataManager().getTimeOfFirstCandleFast(instrument,Period.ONE_HOUR)))
            throw new Failure("restricted","JForex verified archive source or metadata changed; incomplete history was not cached");
    }

    private HourlyFileAttempt readHourlyArchive(com.dukascopy.charts.data.datacache.IFeedDataProvider provider,Instrument instrument,long start,boolean large,BooleanSupplier stop)throws Exception{
        AtomicBoolean active=new AtomicBoolean(true),failed=new AtomicBoolean();
        var progress=new com.dukascopy.charts.data.datacache.ILoadingProgressListener(){
            public void dataLoaded(long from,long to,long current,String information){}
            public void loadingFinished(boolean complete,long from,long to,long current,Throwable error){if(active.get()&&(!complete||error!=null))failed.set(true);}
            public boolean stopJob(){return !active.get()||stop.getAsBoolean();}
        };
        try{
            var rows=provider.getICurvesProtocolHandler().loadChunkFileFromCFG(instrument,Period.ONE_HOUR,OfferSide.BID,start,large,progress);
            return new HourlyFileAttempt(rows,failed.get());
        }finally{active.set(false);}
    }

    private HourlyRecovery recoverHourlyDay(IHistory history, IContext active, Instrument instrument, long from, long until, long exportUntil, boolean[] metadataReloaded, Map<Long, VerifiedHourlyMonth> verifiedMonths) throws Exception {
        Long workDeadline = nativeDownloadDeadline.get();
        final long deadline = workDeadline == null ? System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis) : workDeadline;
        IClient connection = client;
        String account = active.getAccount() == null ? null : active.getAccount().getAccountId();
        if (account == null || account.isBlank()) throw new Failure("restricted", "JForex hourly history metadata is unavailable; incomplete history was not cached");
        requireHourlySource(active, connection, account, deadline);
        IDataService service = active.getDataService();
        if (service == null || service.getClass() != com.dukascopy.api.impl.connect.JForexDataService.class)
            throw new Failure("restricted", "Verified hourly recovery requires the pinned JForex metadata implementation");
        var metadata = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider().getFeedMetadataManager();
        if (metadata == null) throw new Failure("restricted", "JForex hourly history metadata is unavailable; incomplete history was not cached");
        Long first = metadata.getTimeOfFirstCandleFast(instrument, Period.ONE_HOUR);
        if ((first == null || first == Long.MAX_VALUE) && !metadataReloaded[0]) {
            // MAX also represents failed metadata loading in this SDK. Reset
            // only this instrument's RAM entry once within the export budget.
            metadataReloaded[0] = true; metadata.resetTimeOfFirstData(instrument);
            service.getTimeOfFirstCandle(instrument, Period.ONE_HOUR);
            requireHourlySource(active, connection, account, deadline);
            first = metadata.getTimeOfFirstCandleFast(instrument, Period.ONE_HOUR);
        }
        requireHourlySource(active, connection, account, deadline);
        if (!knownFirstHourlyCandle(first, nowMillis.getAsLong()))
            throw new Failure("restricted", "JForex hourly history metadata is unavailable; incomplete history was not cached");
        final long earliest = first, end = until - Period.ONE_HOUR.getInterval();
        long month = com.dukascopy.charts.data.datacache.DataCacheUtils.getChunkStartFast(Period.ONE_HOUR, from);
        if(!verifiedMonths.isEmpty())requireCachedHourlySource(verifiedMonths.values().iterator().next(),active,instrument,deadline);
        if (verifiedMonths.containsKey(month)) return verifiedHourlySlice(verifiedMonths.get(month).bars(), from, until, month);
        CompletableFuture<List<IBar>> result = new CompletableFuture<>();
        List<IBar> bars = new ArrayList<>(); Object lock = new Object(); AtomicBoolean canceled = new AtomicBoolean();
        LoadingDataListener listener = new LoadingDataListener() {
            public void newTick(Instrument ignored, long time, double ask, double bid, double askVolume, double bidVolume) { }
            public void newBar(Instrument item, Period interval, OfferSide side, long time, double open, double close, double low, double high, double volume) {
                synchronized (lock) {
                    if (canceled.get() || result.isDone()) return;
                    try {
                        requireHourlySource(active, connection, account, deadline);
                        IBar bar = new LoadedBar(time, open, high, low, close, volume); validateBar(bar);
                        if (!instrument.equals(item) || !Period.ONE_HOUR.equals(interval) || side != OfferSide.BID || time < from || time > end
                                || time % Period.ONE_HOUR.getInterval() !=0 || time < Math.floorDiv(earliest, Period.ONE_HOUR.getInterval()) * Period.ONE_HOUR.getInterval()
                                || bars.size() >= (until - from) / Period.ONE_HOUR.getInterval() || !bars.isEmpty() && time <= bars.get(bars.size()-1).getTime())
                            throw new Failure("internal", "JForex returned invalid recovered hourly observations");
                        bars.add(bar);
                    } catch (Exception error) { result.completeExceptionally(error); }
                }
            }
        };
        LoadingProgressListener progress = new LoadingProgressListener() {
            public void dataLoaded(long start, long finish, long current, String information) { }
            public void loadingFinished(boolean complete, long start, long finish, long current) {
                synchronized (lock) {
                    if (canceled.get() || result.isDone()) return;
                    if (complete && start == from && finish == end && current == end) result.complete(new ArrayList<>(bars));
                    else result.completeExceptionally(new Failure("offline", "JForex hourly recovery did not completely load its requested UTC day; incomplete history was not cached"));
                }
            }
            public boolean stopJob() { return canceled.get() || closing || System.nanoTime() >= deadline || result.isCompletedExceptionally() || context != active || client != connection || !connection.isConnected()
                || active.getAccount() == null || !Objects.equals(account, active.getAccount().getAccountId()); }
        };
        try {
            requireHourlySource(active, connection, account, deadline);
            history.readBars(instrument, Period.ONE_HOUR, OfferSide.BID, Filter.NO_FILTER, from, end, listener, progress);
            List<IBar> loaded = result.get(Math.max(1, deadline - System.nanoTime()), TimeUnit.NANOSECONDS);
            requireHourlySource(active, connection, account, deadline);
            Long confirmedFirst = metadata.getTimeOfFirstCandleFast(instrument, Period.ONE_HOUR);
            if (!Objects.equals(first, confirmedFirst)) throw new Failure("restricted", "JForex hourly history metadata changed; incomplete history was not cached");
            if (loaded.stream().anyMatch(Bridge::observedHourlyBar)) return new HourlyRecovery(loaded, false);
            // Both ordinary readers can reuse the SDK's falsely empty monthly
            // cache. Use the canonical full-year file first for eligible past
            // years, so a missing monthly file cannot consume the whole budget.
            var provider = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider();
            if (defaultFeedProvider.get() != provider || provider.getICurvesProtocolHandler() == null)
                throw new Failure("restricted", "JForex hourly file source is unavailable or changed; incomplete history was not cached");
            BooleanSupplier stopArchive=()->progress.stopJob()||defaultFeedProvider.get()!=provider;
            Callable<Void> verifyFileSource=()->{
                requireHourlySource(active,connection,account,deadline);
                if(defaultFeedProvider.get()!=provider||!Objects.equals(earliest,metadata.getTimeOfFirstCandleFast(instrument,Period.ONE_HOUR)))
                    throw new Failure("restricted","JForex hourly file metadata or source changed; incomplete history was not cached");
                return null;
            };
            long year=com.dukascopy.charts.data.datacache.DataCacheUtils.getLargeChunkStartFast(Period.ONE_HOUR,from);
            long nextYear=com.dukascopy.charts.data.datacache.DataCacheUtils.getNextLargeChunkStart(Period.ONE_HOUR,year);
            boolean large=earliest<=year&&nextYear<=Math.floorDiv(nowMillis.getAsLong(),Period.DAILY.getInterval())*Period.DAILY.getInterval();
            com.dukascopy.charts.data.datacache.Data[] raw=null;
            HourlyFileAttempt attempt=null;
            if(large){
                verifyFileSource.call();
                attempt=readHourlyArchive(provider,instrument,year,true,stopArchive);raw=attempt.rows();
                verifyFileSource.call();
                // Only unavailable data may try the other canonical granularity.
                // A positive malformed archive must fail validation, not hide.
                if(raw==null||raw.length==0)large=false;
                else if(attempt.failed())throw new Failure("offline","JForex hourly archive loading failed; incomplete history was not cached");
            }
            if(!large){
                verifyFileSource.call();
                attempt=readHourlyArchive(provider,instrument,month,false,stopArchive);raw=attempt.rows();
                verifyFileSource.call();
            }
            if (!large && raw == null && !attempt.failed()) {
                // A null file is a failed SDK download, never an empty market.
                // Retry this exact file once, without renewing the job budget.
                if (deadline-System.nanoTime() <= TimeUnit.SECONDS.toNanos(5)) throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT);
                Thread.sleep(5_000);
                verifyFileSource.call();
                attempt=readHourlyArchive(provider,instrument,month,false,stopArchive);raw=attempt.rows();
                verifyFileSource.call();
            }
            long nextMonth = com.dukascopy.charts.data.datacache.DataCacheUtils.getNextChunkStart(Period.ONE_HOUR, month);
            if (attempt.failed() || raw == null || raw.length ==0)
                throw new Failure("offline", "JForex did not return a complete nonempty hourly BID file; incomplete history was not cached");
            List<IBar> verified=decodedArchiveBID(raw,large,large?year:month,large?nextYear:nextMonth,instrument,provider.getSplitsManager(),provider.getFeedCommissionManager());
            verifyFileSource.call();
            for(long cursor=month;cursor<exportUntil&&cursor<(large?nextYear:nextMonth);cursor=com.dukascopy.charts.data.datacache.DataCacheUtils.getNextChunkStart(Period.ONE_HOUR,cursor)){
                final long low=cursor,high=com.dukascopy.charts.data.datacache.DataCacheUtils.getNextChunkStart(Period.ONE_HOUR,cursor);
                if(!verifiedMonths.containsKey(cursor)&&verifiedMonths.size()>=3)throw new Failure("internal","JForex hourly recovery exceeded its bounded monthly cache");
                verifiedMonths.put(cursor,new VerifiedHourlyMonth(verified.stream().filter(bar->bar.getTime()>=low&&bar.getTime()<high).toList(),provider,earliest,active,connection,account));
            }
            hourlyFileRecoveries.incrementAndGet();
            return verifiedHourlySlice(verifiedMonths.get(month).bars(), from, until, month);
        } catch (TimeoutException error) { throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT); }
        catch (ExecutionException error) { if (error.getCause() instanceof Failure failure) throw failure; throw error; }
        finally { synchronized (lock) { canceled.set(true); } }
    }

    private static boolean observedHourlyBar(IBar bar) {
        return bar.getVolume() !=0 || bar.getOpen() != bar.getClose() || bar.getHigh() != bar.getLow();
    }

    // Canonical SDK large H1 files contain ASK/BID pairs, in that order.
    // Validate the complete decoded payload before selecting or adjusting BID.
    static List<IBar> decodedArchiveBID(com.dukascopy.charts.data.datacache.Data[] raw, boolean large, long from, long until,
            Instrument instrument, com.dukascopy.charts.data.datacache.splits.ISplitsManager splits,
            com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager commissions) {
        long hour=Period.ONE_HOUR.getInterval(); int stride=large?2:1;
        if(raw==null||raw.length==0||raw.length%stride!=0||raw.length>(until-from)/hour*stride||large&&raw.length!=(until-from)/hour*stride)
            throw new Failure("offline","JForex did not return a complete nonempty hourly archive");
        if(splits==null||commissions==null)throw new Failure("restricted","JForex hourly price adjustments are unavailable");
        var selected=new com.dukascopy.charts.data.datacache.Data[raw.length/stride];long[] originalTimes=new long[selected.length];long previous=-1;
        for(int index=0;index<raw.length;index+=stride){
            long time=-1;
            for(int side=0;side<stride;side++){
                if(!(raw[index+side] instanceof IBar bar))throw new Failure("internal","JForex archive returned an invalid candle type");
                validateBar(bar);
                if(bar.getTime()<from||bar.getTime()>=until||bar.getTime()%hour!=0||side>0&&bar.getTime()!=time)
                    throw new Failure("internal","JForex archive returned invalid candle bounds or side pairing");
                time=bar.getTime();
            }
            if(time<=previous)throw new Failure("internal","JForex archive returned duplicate or unordered hourly observations");
            if(large&&time!=from+(long)(index/stride)*hour)throw new Failure("internal","JForex yearly archive did not cover every paired hourly slot");
            previous=time;IBar bid=(IBar)raw[index+stride-1];
            var copy=new com.dukascopy.charts.data.datacache.CandleData(time,bid.getOpen(),bid.getClose(),bid.getLow(),bid.getHigh(),bid.getVolume());
            copy.feedCommissionApplied=raw[index+stride-1].feedCommissionApplied;selected[index/stride]=copy;originalTimes[index/stride]=time;
        }
        splits.applySplitsToData(instrument,selected);
        var adjusted=commissions.applyFeedCommissionToData(instrument,Period.ONE_HOUR,OfferSide.BID,selected);
        if(adjusted==null||adjusted.length!=selected.length)throw new Failure("internal","JForex archive adjustments changed its candle count");
        List<IBar> result=new ArrayList<>();previous=-1;
        for(int index=0;index<adjusted.length;index++){
            if(!(adjusted[index] instanceof IBar bar))throw new Failure("internal","JForex archive adjustments returned an invalid candle type");
            IBar copy=new LoadedBar(bar.getTime(),bar.getOpen(),bar.getHigh(),bar.getLow(),bar.getClose(),bar.getVolume());validateBar(copy);
            if(copy.getTime()!=originalTimes[index]||copy.getTime()<from||copy.getTime()>=until||copy.getTime()%hour!=0||copy.getTime()<=previous)
                throw new Failure("internal","JForex archive adjustments changed candle times");
            previous=copy.getTime();result.add(copy);
        }
        if(result.stream().noneMatch(Bridge::observedHourlyBar))throw new Failure("offline","JForex archive contained no observed BID candles");
        return result;
    }

    private Object historyArchiveDiagnostic(JsonObject request) throws Exception {
        requireConnected();Feed feed=feed(request.getAsJsonObject("feedKey"));
        long from=optionalLong(request,"from")*1000,to=optionalLong(request,"to")*1000,hour=Period.ONE_HOUR.getInterval(),day=Period.DAILY.getInterval();
        String archive=text(request,"archive",16);boolean large=archive.equals("year");
        if(feed.period!=Period.ONE_HOUR||!Set.of("month","year").contains(archive)||from<0||from%day!=0||to-from!=day||to>Math.floorDiv(nowMillis.getAsLong(),day)*day)
            throw new Failure("internal","Hourly archive diagnostics require one closed UTC source day");
        long start=large?com.dukascopy.charts.data.datacache.DataCacheUtils.getLargeChunkStartFast(Period.ONE_HOUR,from):com.dukascopy.charts.data.datacache.DataCacheUtils.getChunkStartFast(Period.ONE_HOUR,from);
        long finish=large?com.dukascopy.charts.data.datacache.DataCacheUtils.getNextLargeChunkStart(Period.ONE_HOUR,start):com.dukascopy.charts.data.datacache.DataCacheUtils.getNextChunkStart(Period.ONE_HOUR,start);
        if(large&&finish>Math.floorDiv(nowMillis.getAsLong(),day)*day)throw new Failure("restricted","Yearly archive diagnostics require a complete past UTC year");
        IContext active=context;IClient connection=client;String account=active.getAccount()==null?null:active.getAccount().getAccountId();
        if(account==null||account.isBlank())throw new Failure("restricted","JForex source account is unavailable");
        Long configuredDeadline=nativeDownloadDeadline.get();long deadline=configuredDeadline==null?System.nanoTime()+TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis):configuredDeadline;
        synchronized(instrumentLock){historyUsers.merge(feed.instrument,1,Integer::sum);}
        try{
            ensureInstrument(feed.instrument);
            while(!active.getSubscribedInstruments().contains(feed.instrument)){requireHourlySource(active,connection,account,deadline);Thread.sleep(10);}
            requireHourlySource(active,connection,account,deadline);
            if(!(active.getDataService() instanceof com.dukascopy.api.impl.connect.JForexDataService service))throw new Failure("restricted","Pinned JForex archive source is unavailable");
            var provider=service.getFeedDataProvider();var metadata=provider.getFeedMetadataManager();
            if(provider!=defaultFeedProvider.get()||metadata==null||provider.getICurvesProtocolHandler()==null)throw new Failure("restricted","JForex archive source changed or is unavailable");
            Long first=metadata.getTimeOfFirstCandleFast(feed.instrument,Period.ONE_HOUR);
            if(first==null||first==Long.MAX_VALUE){metadata.resetTimeOfFirstData(feed.instrument);service.getTimeOfFirstCandle(feed.instrument,Period.ONE_HOUR);first=metadata.getTimeOfFirstCandleFast(feed.instrument,Period.ONE_HOUR);}
            requireHourlySource(active,connection,account,deadline);
            if(!knownFirstHourlyCandle(first,nowMillis.getAsLong()))throw new Failure("restricted","JForex hourly archive metadata is unavailable");
            AtomicBoolean failed=new AtomicBoolean();
            var progress=new com.dukascopy.charts.data.datacache.ILoadingProgressListener(){
                public void dataLoaded(long a,long b,long c,String information){}
                public void loadingFinished(boolean complete,long a,long b,long c,Throwable error){if(!complete||error!=null)failed.set(true);}
                public boolean stopJob(){return closing||Thread.currentThread().isInterrupted()||System.nanoTime()>=deadline||context!=active||client!=connection||!connection.isConnected()||provider!=defaultFeedProvider.get();}
            };
            var raw=provider.getICurvesProtocolHandler().loadChunkFileFromCFG(feed.instrument,Period.ONE_HOUR,OfferSide.BID,start,large,progress);
            requireHourlySource(active,connection,account,deadline);
            if(provider!=defaultFeedProvider.get()||!Objects.equals(first,metadata.getTimeOfFirstCandleFast(feed.instrument,Period.ONE_HOUR)))throw new Failure("restricted","JForex archive metadata or source changed");
            Map<String,Object> result=new LinkedHashMap<>();result.put("instrument",feed.instrument.name());result.put("archive",archive);result.put("start",start);result.put("end",finish);
            result.put("nullResponse",raw==null);result.put("rawRows",raw==null?0:raw.length);result.put("failed",failed.get());result.put("firstHourFast",first.toString());
            if(raw==null||raw.length==0||failed.get()){result.put("bars",List.of());result.put("fullOpenBars",List.of());return result;}
            int zeroPrices=0,zeroFlat=0;
            for(var entry:raw)if(entry instanceof IBar bar){
                if(bar.getOpen()==0&&bar.getHigh()==0&&bar.getLow()==0&&bar.getClose()==0&&bar.getVolume()==0)zeroPrices++;
                if(!observedHourlyBar(bar))zeroFlat++;
            }
            result.put("zeroPriceRows",zeroPrices);result.put("zeroFlatRows",zeroFlat);
            List<IBar> decoded;
            try{decoded=decodedArchiveBID(raw,large,start,finish,feed.instrument,provider.getSplitsManager(),provider.getFeedCommissionManager());}
            catch(Failure error){result.put("decodeError",error.getMessage());result.put("bars",List.of());result.put("fullOpenBars",List.of());return result;}
            List<IBar> target=decoded.stream().filter(bar->bar.getTime()>=from&&bar.getTime()<to&&observedHourlyBar(bar)).toList();
            List<long[]> closures=verifiedHistoricalClosures(service,feed.instrument,from,to);
            result.put("bars",target.stream().map(bar->candle(bar,true)).toList());
            result.put("fullOpenBars",target.stream().filter(bar->!intersectsClosure(closures,bar.getTime(),bar.getTime()+hour)).map(bar->candle(bar,true)).toList());
            return result;
        }finally{synchronized(instrumentLock){historyUsers.computeIfPresent(feed.instrument,(instrument,count)->count==1?null:count-1);releaseInstruments();}}
    }
    private HourlyRecovery verifiedHourlySlice(List<IBar> monthBars, long from, long until, long month) throws Exception {
        long nextMonth = com.dukascopy.charts.data.datacache.DataCacheUtils.getNextChunkStart(Period.ONE_HOUR, month);
        boolean completePastMonth = nextMonth <= Math.floorDiv(nowMillis.getAsLong(), Period.DAILY.getInterval()) * Period.DAILY.getInterval();
        return new HourlyRecovery(monthBars.stream().filter(bar -> bar.getTime() >=from && bar.getTime() <until).toList(), completePastMonth);
    }

    private static boolean intersectsClosure(List<long[]> closures, long from, long to) {
        for (long[] closure : closures) if (closure[0] < to && closure[1] > from) return true;
        return false;
    }
    private static boolean hasFullOpenHour(List<long[]> closures, long from, long to, long hour) {
        for (long cursor = from; cursor < to; cursor += hour) if (!intersectsClosure(closures, cursor, cursor + hour)) return true;
        return false;
    }

    private static boolean entirelyClosed(List<long[]> closures, long from, long to) {
        List<long[]> sorted = new ArrayList<>(closures); sorted.sort(Comparator.comparingLong(value -> value[0]));
        long covered = from;
        for (long[] interval : sorted) {
            if (interval[1] <= covered) continue;
            if (interval[0] > covered) return false;
            covered = interval[1]; if (covered >= to) return true;
        }
        return false;
    }

    private static boolean historicalCalendarPending(IDataService service, Instrument instrument) {
        if (service == null || service.getClass() != com.dukascopy.api.impl.connect.JForexDataService.class) return false;
        var calendar = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider().getHolidayScheduleService();
        return calendar != null && (!calendar.hasHolidaysScheduleLoaded(instrument) || calendar.getHolidaySchedule(instrument) == null);
    }

    // The public API conflates an unloaded calendar with no closures. This
    // read-only adapter is limited to the checksum-pinned SDK implementation;
    // its actual READ schedule must be loaded and valid for this historical
    // range before an empty interval list can mean unrestricted observations.
    static List<long[]> verifiedHistoricalClosures(IDataService service, Instrument instrument, long from, long to) {
        if (service == null || service.getClass() != com.dukascopy.api.impl.connect.JForexDataService.class)
            throw new Failure("restricted", "Verified hourly history requires the pinned JForex historical calendar implementation");
        var calendar = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider().getHolidayScheduleService();
        boolean loaded = calendar != null && calendar.hasHolidaysScheduleLoaded(instrument);
        var schedule = loaded ? calendar.getHolidaySchedule(instrument) : null;
        if (schedule == null) throw new Failure("restricted", "JForex historical calendar is unavailable for " + instrument.name() + " (loaded=" + loaded + ", readSchedule=false)");
        List<com.dukascopy.holiday.CustomHolidayScheduleDetail> rules = new ArrayList<>();
        rules.addAll(schedule.getRegularHourlyHolidayScheduleDetails()); rules.addAll(schedule.getRegularDailyHolidayScheduleDetails());
        rules.addAll(schedule.getRegularWeeklyHolidayScheduleDetails()); rules.addAll(schedule.getRegularMonthlyHolidayScheduleDetails()); rules.addAll(schedule.getRegularYearlyHolidayScheduleDetails());
        java.time.Instant start = java.time.Instant.ofEpochMilli(from), finish = java.time.Instant.ofEpochMilli(to);
        if (rules.isEmpty()) {
            // In the pinned SDK a custom-only READ schedule lists absolute
            // closures. HolidayScheduleImpl.isHoliday returns false outside
            // those dates; these are not opening exceptions in a closed
            // baseline. A loaded, non-null READ object is required above.
            for (var rule : schedule.getCustomHolidayScheduleDetails()) {
                var interval = rule == null ? null : rule.getDetailInterval();
                if (interval == null || !interval.getStart().isBefore(interval.getEnd()))
                    throw new Failure("restricted", "JForex historical calendar returned an invalid absolute closure");
            }
        } else {
            List<org.threeten.extra.Interval> validity = rules.stream().map(com.dukascopy.holiday.CustomHolidayScheduleDetail::getDetailInterval)
                .filter(Objects::nonNull).sorted(Comparator.comparing(org.threeten.extra.Interval::getStart)).toList();
            java.time.Instant covered = start;
            for (var interval : validity) {
                if (interval.getStart().isAfter(covered)) break;
                if (interval.getEnd().isAfter(covered)) covered = interval.getEnd();
            }
            if (covered.isBefore(finish)) throw new Failure("restricted", "JForex historical calendar does not cover the requested dates");
        }
        Set<ITimeDomain> domains = service.getOfflineTimeDomains(from, to, instrument);
        if (domains == null || domains.size() > 10_000) throw new Failure("restricted", "JForex historical calendar returned invalid intervals");
        List<long[]> result = new ArrayList<>();
        for (ITimeDomain domain : domains) {
            if (domain == null || domain.getEnd() <= domain.getStart()) throw new Failure("restricted", "JForex historical calendar returned invalid intervals");
            result.add(new long[]{domain.getStart(), domain.getEnd()});
        }
        return result;
    }

    private Object historyCalendar(JsonObject request) throws Exception {
        requireConnected(); Feed feed = feed(request.getAsJsonObject("feedKey"));
        long from = optionalLong(request, "from"), to = optionalLong(request, "to"), hour = 3600;
        if (feed.period != Period.ONE_HOUR || from < 0 || to <= from || from % hour != 0 || to % hour != 0 || to - from > 1000 * hour || to > Math.floorDiv(nowMillis.getAsLong(), hour * 1000) * hour)
            throw new Failure("internal", "Calendar diagnostics require at most1000closed UTC hours");
        synchronized (instrumentLock) { historyUsers.merge(feed.instrument, 1, Integer::sum); }
        try {
            ensureInstrument(feed.instrument);
            long readyBy = System.nanoTime() + TimeUnit.SECONDS.toNanos(2);
            Map<String, Object> result;
            do {
                if (closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
                result = historicalCalendarDiagnostic(context.getDataService(), feed.instrument, from * 1000, to * 1000);
                if (Boolean.TRUE.equals(result.get("loaded"))) break;
                Thread.sleep(10);
            } while (System.nanoTime() < readyBy);
            return result;
        } finally {
            synchronized (instrumentLock) { historyUsers.computeIfPresent(feed.instrument, (instrument, count) -> count == 1 ? null : count - 1); releaseInstruments(); }
        }
    }

    private Object historyCandleDiagnostic(JsonObject request) throws Exception {
        requireConnected(); Feed feed = feed(request.getAsJsonObject("feedKey"));
        long from = optionalLong(request, "from"), to = optionalLong(request, "to"), hour = 3600;
        String filterName = text(request, "filter", 32);
        if (feed.period != Period.ONE_HOUR || from < 0 || to <= from || from % hour != 0 || to % hour != 0 || to - from > 1000 * hour || to > Math.floorDiv(nowMillis.getAsLong(), hour * 1000) * hour || !Set.of("WEEKENDS", "NO_FILTER").contains(filterName))
            throw new Failure("internal", "Hourly diagnostics require bounded closed hours and a supported filter");
        synchronized (instrumentLock) { historyUsers.merge(feed.instrument, 1, Integer::sum); }
        try {
            ensureInstrument(feed.instrument);
            long readyBy = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis);
            while (!context.getSubscribedInstruments().contains(feed.instrument)) {
                if (closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
                if (System.nanoTime() > readyBy) throw new Failure("timeout", HOURLY_DOWNLOAD_TIMEOUT);
                Thread.sleep(10);
            }
            IHistory history = context.getHistory();
            Map<String, Object> metadataBefore = hourlyMetadataDiagnostic(context.getDataService(), feed.instrument);
            List<IBar> bars = history.getBars(feed.instrument, Period.ONE_HOUR, OfferSide.BID, Filter.valueOf(filterName), from * 1000, (to - hour) * 1000);
            if (bars == null || bars.size() > 1000) throw new Failure("internal", "Hourly diagnostic exceeded its bar bounds");
            int zeroVolume = 0, zeroFlat = 0;
            for (IBar bar : bars) {
                validateBar(bar);
                if (bar.getVolume() ==0) { zeroVolume++; if (bar.getOpen() == bar.getClose() && bar.getHigh() == bar.getLow()) zeroFlat++; }
            }
            Map<String, Object> result = new LinkedHashMap<>();
            result.put("instrument", feed.instrument.name()); result.put("filter", filterName); result.put("from", from); result.put("to", to);
            result.put("rows", bars.size()); result.put("zeroVolumeRows", zeroVolume); result.put("zeroFlatRows", zeroFlat);
            result.put("first", bars.isEmpty() ? 0L : bars.get(0).getTime() / 1000); result.put("last", bars.isEmpty() ? 0L : bars.get(bars.size()-1).getTime() / 1000);
            result.put("metadataBefore", metadataBefore); result.put("metadataAfter", hourlyMetadataDiagnostic(context.getDataService(), feed.instrument));
            result.put("verifiedMonthlyFileRecoveries", hourlyFileRecoveries.get());
            return result;
        } finally {
            synchronized (instrumentLock) { historyUsers.computeIfPresent(feed.instrument, (instrument, count) -> count == 1 ? null : count - 1); releaseInstruments(); }
        }
    }

    private static Map<String, Object> hourlyMetadataDiagnostic(IDataService service, Instrument instrument) {
        Map<String, Object> result = new LinkedHashMap<>();
        boolean pinned = service != null && service.getClass() == com.dukascopy.api.impl.connect.JForexDataService.class;
        result.put("pinnedImplementation", pinned);
        if (pinned) {
            var metadata = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider().getFeedMetadataManager();
            Long first = metadata == null ? null : metadata.getTimeOfFirstCandleFast(instrument, Period.ONE_HOUR);
            result.put("firstHourFast", first == null ? "unloaded" : first == Long.MAX_VALUE ? "MAX" : first.toString());
        }
        return result;
    }

    static Map<String, Object> historicalCalendarDiagnostic(IDataService service, Instrument instrument, long from, long to) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("instrument", instrument.name()); result.put("from", from); result.put("to", to);
        boolean supported = service != null && service.getClass() == com.dukascopy.api.impl.connect.JForexDataService.class;
        result.put("pinnedImplementation", supported);
        if (!supported) return result;
        var calendar = ((com.dukascopy.api.impl.connect.JForexDataService) service).getFeedDataProvider().getHolidayScheduleService();
        boolean loaded = calendar != null && calendar.hasHolidaysScheduleLoaded(instrument);
        var schedule = loaded ? calendar.getHolidaySchedule(instrument) : null;
        result.put("loaded", loaded); result.put("readSchedule", schedule != null);
        if (schedule == null) return result;
        List<Object> rules = new ArrayList<>();
        diagnosticRules(rules, "custom", schedule.getCustomHolidayScheduleDetails());
        diagnosticRules(rules, "hourly", schedule.getRegularHourlyHolidayScheduleDetails()); diagnosticRules(rules, "daily", schedule.getRegularDailyHolidayScheduleDetails());
        diagnosticRules(rules, "weekly", schedule.getRegularWeeklyHolidayScheduleDetails()); diagnosticRules(rules, "monthly", schedule.getRegularMonthlyHolidayScheduleDetails()); diagnosticRules(rules, "yearly", schedule.getRegularYearlyHolidayScheduleDetails());
        result.put("rules", rules);
        List<Object> domains = new ArrayList<>();
        Set<ITimeDomain> raw = service.getOfflineTimeDomains(from, to, instrument);
        result.put("closedIntervalCount", raw == null ? -1 : raw.size());
        if (raw != null) for (ITimeDomain domain : raw) { if (domains.size() ==64) break; domains.add(List.of(domain.getStart(), domain.getEnd())); }
        result.put("closedIntervals", domains); return result;
    }

    private static void diagnosticRules(List<Object> result, String kind, Collection<? extends com.dukascopy.holiday.CustomHolidayScheduleDetail> rules) {
        for (var rule : rules) {
            if (result.size() == 128) return;
            var interval = rule.getDetailInterval();
            Map<String, Object> item = new LinkedHashMap<>(); item.put("kind", kind);
            item.put("from", interval == null ? "null" : interval.getStart().toString()); item.put("to", interval == null ? "null" : interval.getEnd().toString());
            item.put("timeZone", rule.getTimezone() == null ? "null" : rule.getTimezone().getId());
            if (rule instanceof com.dukascopy.holiday.RegularHourlyHolidayScheduleDetail value) { item.put("startMinute", value.getStartMinute()); item.put("endMinute", value.getEndMinute()); }
            if (rule instanceof com.dukascopy.holiday.RegularDailyHolidayScheduleDetail value) { item.put("startHour", value.getStartHour()); item.put("endHour", value.getEndHour()); }
            if (rule instanceof com.dukascopy.holiday.RegularWeeklyHolidayScheduleDetail value) { item.put("startDayOfWeek", value.getStartDayOfWeek()); item.put("endDayOfWeek", value.getEndDayOfWeek()); }
            result.add(item);
        }
    }

    private static BasicFileAttributes tickOutputAttributes(Path path, boolean empty) throws IOException {
        if (!path.isAbsolute() || !path.normalize().equals(path))
            throw new Failure("internal", "JForex tick output must be an absolute private file path");
        BasicFileAttributes attributes = Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
        if (!attributes.isRegularFile()
                || attributes.isSymbolicLink() || empty && attributes.size() != 0 || attributes.size() > TICK_EXPORT_MAX_BYTES)
            throw new Failure("internal", "JForex tick output must be an existing empty regular private file");
        if (Files.getFileAttributeView(path, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS) != null
                && !Files.getPosixFilePermissions(path, LinkOption.NOFOLLOW_LINKS).equals(PosixFilePermissions.fromString("rw-------")))
            throw new Failure("internal", "JForex tick output permissions must be 0600");
        return attributes;
    }

    private Object historyTicks(JsonObject request) throws Exception {
        requireConnected();
        String token = text(request, "exportId", 128);
        TickExport export;
        synchronized (tickExportLock) { export = tickExports.get(token); }
        if (export == null) throw new Failure("internal", "JForex tick exports require bounded asynchronous dispatch");
        Instrument instrument = tradeInstrument(request);
        long from = optionalLong(request, "from"), to = optionalLong(request, "to");
        if (from < 0 || to <= from || from % TICK_HOUR != 0 || to - from != TICK_HOUR
                || to > Math.floorDiv(nowMillis.getAsLong(), TICK_HOUR) * TICK_HOUR)
            throw new Failure("internal", "JForex tick history requires exactly one closed UTC hour");
        Path path = Path.of(text(request, "output", 4096));
        BasicFileAttributes original = tickOutputAttributes(path, true);
        IContext activeContext = context;
        synchronized (instrumentLock) { historyUsers.merge(instrument, 1, Integer::sum); }
        try {
            export.check(); ensureInstrument(instrument);
            long readyBy = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(tickHistoryTimeoutMillis);
            while (activeContext != null && !activeContext.getSubscribedInstruments().contains(instrument)) {
                export.check();
                if (System.nanoTime() >= readyBy) throw new Failure("timeout", "JForex tick history subscription timed out");
                Thread.sleep(10);
            }
            if (activeContext == null || activeContext != context) throw new Failure("offline", "The JForex history session changed");
            export.check();
            // The SDK's upper endpoint is inclusive; the exported hour is
            // strictly [from,to), preserving duplicate millisecond observations.
            List<ITick> ticks = activeContext.getHistory().getTicks(instrument, from, to - 1);
            export.check();
            if (ticks == null || ticks.size() > TICK_EXPORT_MAX_ROWS)
                throw new Failure("internal", "JForex tick history exceeded its bounded row limit");
            BasicFileAttributes current = tickOutputAttributes(path, true);
            if (!Objects.equals(original.fileKey(), current.fileKey()))
                throw new Failure("internal", "JForex tick output was replaced");
            try (FileChannel file = FileChannel.open(path, StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS)) {
                export.attach(file);
                long bytes = writeTickLine(file, "timestamp,askPrice,bidPrice,askVolume,bidVolume\n", 0);
                long previous = -1, rows = 0;
                for (ITick tick : ticks) {
                    export.check();
                    if (tick == null) throw new Failure("internal", "JForex returned a missing historical tick");
                    long at = tick.getTime(); double ask = tick.getAsk(), bid = tick.getBid(), askVolume = tick.getAskVolume(), bidVolume = tick.getBidVolume();
                    if (at < from || at >= to || at < previous || !finite(ask, bid, askVolume, bidVolume)
                            || ask <= 0 || bid <= 0 || askVolume < 0 || bidVolume < 0)
                        throw new Failure("internal", "JForex returned invalid historical tick values or ordering");
                    bytes = writeTickLine(file, at + "," + ask + "," + bid + "," + askVolume + "," + bidVolume + "\n", bytes);
                    previous = at; rows++;
                }
                export.check(); file.force(true); export.check();
                return Map.of("rows", rows);
            }
        } finally {
            synchronized (instrumentLock) {
                historyUsers.computeIfPresent(instrument, (item, count) -> count == 1 ? null : count - 1);
                releaseInstruments();
            }
        }
    }
    private static long writeTickLine(FileChannel file, String line, long written) throws IOException {
        byte[] bytes = line.getBytes(StandardCharsets.UTF_8);
        if (written > TICK_EXPORT_MAX_BYTES - bytes.length)
            throw new Failure("internal", "JForex tick history exceeded its 64 MiB output limit");
        ByteBuffer buffer = ByteBuffer.wrap(bytes);
        while (buffer.hasRemaining()) file.write(buffer);
        return written + bytes.length;
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
            // Fill the unfinished long candle from stored basic candles, not a
            // cold current DAILY/WEEKLY read that may reconstruct the whole
            // day/week from finer data. Closed target candles remain native.
            synchronized (sub) {
                if (subscriptions.get(sub.id) != sub || closing || Thread.currentThread().isInterrupted() || sub.seedGeneration.get() != generation) return;
                sub.loadingCurrent = true;
                health(sub.id, "reconciling", loadingReason(sub), 0);
            }
            List<IBar> active = readCurrentBars(history, sub.instrument, sub.period, current);
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
        Map<Long, Long> quoteFloors = new HashMap<>();
        for (IBar bar : source) if (!inactiveFlat(sub.instrument, bar, current)) {
            JsonObject value = candle(bar, bar.getTime() < current);
            // A late seed cannot supersede newer SDK ticks or closed-bar events.
            value.addProperty("sourceRevision", seedRevision); bars.put(bar.getTime(), value);
            if (bar instanceof CurrentBar contextBar) quoteFloors.put(bar.getTime(), contextBar.quoteFloor());
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
                        // SDK bars expose only their bucket start, not the last
                        // trade time. A tick from an earlier source bucket can
                        // add known extremes but cannot rewind the newer close.
                        if (sub.lastTickTime >= quoteFloors.getOrDefault(sub.lastTime, 0L))
                            merged.addProperty("close", sub.last.get("close").getAsDouble());
                        merged.addProperty("quality", "provisional"); merged.addProperty("sourceRevision", nextRevision());
                        bars.put(sub.lastTime, merged);
                    } else if (previous == null || sub.last.get("closed").getAsBoolean()) bars.put(sub.lastTime, sub.last);
                }
                long last = bars.lastKey(), sourceFloor = quoteFloors.getOrDefault(last, 0L);
                sub.quoteFloor = last == sub.lastTime ? Math.max(sub.quoteFloor, sourceFloor) : sourceFloor;
                sub.lastTime = last; sub.last = bars.lastEntry().getValue();
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
        return sub.loadingCurrent ? "Historical JForex Bid candles loaded; building current candle from hourly/minute bars"
            : "Loading JForex Bid historical candles; live quotes remain subscribed";
    }
    private List<IBar> readCurrentBars(IHistory history, Instrument instrument, Period period, long current) throws Exception {
        // Freeze the source ranges. If the target closes during these loads,
        // callers refetch its final native bar before advancing any checkpoint.
        long cutoff = Math.min(nowMillis.getAsLong(), current + period.getInterval() - 1);
        long day = history.getBarStart(Period.DAILY, cutoff);
        long hour = history.getBarStart(Period.ONE_HOUR, cutoff);
        long minute = history.getBarStart(Period.ONE_MIN, cutoff);
        TreeMap<Long, IBar> components = new TreeMap<>();
        if (period.equals(Period.WEEKLY) && current < day)
            collectCurrentBars(components, history, instrument, Period.DAILY, current, day - Period.DAILY.getInterval());
        if (day < hour)
            collectCurrentBars(components, history, instrument, Period.ONE_HOUR, day, hour - Period.ONE_HOUR.getInterval());
        // At most sixty M1 bars, including only the one currently forming
        // minute. No current H1, D1, W1 or historical tick request is made.
        collectCurrentBars(components, history, instrument, Period.ONE_MIN, hour, minute);
        if (components.isEmpty()) return List.of();
        double high = -Double.MAX_VALUE, low = Double.MAX_VALUE;
        BigDecimal volume = BigDecimal.ZERO;
        for (IBar bar : components.values()) {
            high = Math.max(high, bar.getHigh()); low = Math.min(low, bar.getLow());
            volume = volume.add(BigDecimal.valueOf(bar.getVolume()));
        }
        IBar aggregate = new LoadedBar(current, components.firstEntry().getValue().getOpen(), high, low,
            components.lastEntry().getValue().getClose(), volume.doubleValue());
        validateBar(aggregate);
        return List.of(new CurrentBar(aggregate, components.lastKey()));
    }
    private void collectCurrentBars(TreeMap<Long, IBar> components, IHistory history, Instrument instrument, Period period, long from, long end) throws Exception {
        for (IBar bar : readRangeBars(history, instrument, period, from, end)) {
            validateBar(bar);
            if (history.getBarStart(period, bar.getTime()) != bar.getTime())
                throw new Failure("internal", "JForex returned an unaligned current-candle component");
            // A filtered current M1 can still be a flat closed-market SDK
            // placeholder. It must not replace a real weekly last close.
            if (period.equals(Period.ONE_MIN) && inactiveFlat(instrument, bar, end)) continue;
            components.put(bar.getTime(), bar);
        }
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
        long workTimeout = nativeHistoryDownload.get() ? tickHistoryTimeoutMillis : historyTimeoutMillis;
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
            long readyBy = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(workTimeout);
            while (activeContext != null && !activeContext.getSubscribedInstruments().contains(instrument)) {
                if (closing || Thread.currentThread().isInterrupted()) throw new InterruptedException();
                if (System.nanoTime() >= readyBy) throw new TimeoutException();
                Thread.sleep(10);
            }
            if (activeContext == null || closing) throw new InterruptedException();
            if (range) history.readBars(instrument, period, OfferSide.BID, Filter.WEEKENDS, from, end, listener, progress);
            else history.readBars(instrument, period, OfferSide.BID, Filter.WEEKENDS, limit, end, 0, listener, progress);
            return result.get(workTimeout, TimeUnit.MILLISECONDS);
        } catch (TimeoutException error) {
            throw new Failure("timeout", nativeHistoryDownload.get() ? NATIVE_DOWNLOAD_TIMEOUT : "JForex historical data loading timed out. Retry the chart to reload history.");
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
    private ITick freshTradeQuote(Instrument instrument) throws Exception {
        return tradeQuote(instrument, false);
    }
    private ITick tradeQuote(Instrument instrument, boolean referenceAllowed) throws Exception {
        ensureInstrument(instrument);
        ITick tick = context.getHistory().getLastTick(instrument);
        long now = nowMillis.getAsLong();
        if (tick == null || tick.getTime() <= 0 || now - tick.getTime() > (referenceAllowed ? REFERENCE_QUOTE_MAX_AGE : 30_000) || tick.getTime() > now + 5_000
                || !Double.isFinite(tick.getBid()) || !Double.isFinite(tick.getAsk()) || tick.getBid() <= 0 || tick.getAsk() < tick.getBid())
            throw new Failure("restricted", referenceAllowed ? "No valid JForex Bid/Ask quote from the last 14 days is available for a planning estimate" : "A fresh JForex Bid/Ask quote is required; wait for the market to open");
        return tick;
    }
    private static record CurrencyRoute(String currency, List<Instrument> instruments) { }
    private List<Instrument> conversionRoute(String from, String to) {
        if (from.equals(to)) return List.of();
        ArrayDeque<CurrencyRoute> queue = new ArrayDeque<>();
        Set<String> visited = new HashSet<>(); queue.add(new CurrencyRoute(from, List.of())); visited.add(from);
        while (!queue.isEmpty()) {
            CurrencyRoute current = queue.remove();
            if (current.instruments().size() >= 2) continue;
            for (Instrument item : available) {
                if (!isFiatForex(item)) continue;
                String base = item.getPrimaryJFCurrency().getCurrencyCode(), quote = item.getSecondaryJFCurrency().getCurrencyCode();
                String next = current.currency().equals(base) ? quote : current.currency().equals(quote) ? base : "";
                if (next.isEmpty() || !visited.add(next)) continue;
                List<Instrument> route = new ArrayList<>(current.instruments()); route.add(item);
                if (next.equals(to)) return route;
                queue.add(new CurrencyRoute(next, route));
            }
        }
        throw new Failure("restricted", "No current account-currency conversion market is available");
    }
    private ICurrency accountCurrency() {
        IAccount account = context.getAccount();
        if (account == null) throw new Failure("restricted", "The connected JForex account snapshot is unavailable");
        ICurrency currency = null;
        try { currency = account.getAccountCurrency(); } catch (Exception ignored) { }
        if (currency == null) {
            // Both values are supplied by the SDK. Older account implementations
            // can populate the original Java Currency accessor only.
            try { if (account.getCurrency() != null) currency = JFCurrency.getInstance(account.getCurrency().getCurrencyCode()); } catch (Exception ignored) { }
        }
        if (currency == null || currency.getCurrencyCode() == null || !currency.getCurrencyCode().matches("[A-Z][A-Z0-9]{2,7}"))
            throw new Failure("restricted", "JForex returned no account currency through either supported account accessor");
        return currency;
    }
    private static boolean boundedConversion(double rate, double lower, double upper) {
        return Double.isFinite(rate) && rate > 0 && rate >= lower * (1 - 1e-8) && rate <= upper * (1 + 1e-8);
    }
    private Map<String, Object> tradeValuation(Instrument instrument, long entryTime) {
        return tradeValuation(instrument, entryTime, false);
    }
    private Map<String, Object> tradeValuation(Instrument instrument, long entryTime, boolean referenceAllowed) {
        Map<String, Object> result = new HashMap<>();
        result.put("accountCurrency", ""); result.put("profitPerPriceUnit", 0); result.put("valuationTime", referenceAllowed ? entryTime : 0);
        Set<Instrument> conversions = new HashSet<>();
        synchronized (instrumentLock) { historyUsers.merge(instrument, 1, Integer::sum); }
        String stage = "Account currency";
        try {
            ICurrency currency = accountCurrency();
            result.put("accountCurrency", currency.getCurrencyCode());
            ICurrency quoteCurrency = instrument.getSecondaryJFCurrency();
            if (quoteCurrency == null) throw new Failure("restricted", "The instrument quote currency is unavailable");
            String sourceCurrency = quoteCurrency.getCurrencyCode();
            stage = "Conversion " + sourceCurrency + " to " + currency.getCurrencyCode();
            long valuedAt = entryTime;
            double lowerRate = 1, upperRate = 1, midpointRate = 1;
            for (Instrument conversion : conversionRoute(sourceCurrency, currency.getCurrencyCode())) {
                if (!conversion.equals(instrument) && conversions.add(conversion)) synchronized (instrumentLock) { historyUsers.merge(conversion, 1, Integer::sum); }
                ITick current;
                try { current = tradeQuote(conversion, referenceAllowed); }
                catch (Exception error) { throw new Failure("restricted", referenceAllowed ? "No valid recent conversion quote for " + conversion.name() + "; account-currency amount is unavailable" : "No fresh conversion quote for " + conversion.name() + "; wait for the FX market or its subscription to update"); }
                valuedAt = Math.min(valuedAt, current.getTime());
                if (referenceAllowed) result.put("valuationTime", valuedAt);
                double mid = current.getBid() + (current.getAsk() - current.getBid()) / 2;
                if (sourceCurrency.equals(conversion.getPrimaryJFCurrency().getCurrencyCode())) {
                    lowerRate *= current.getBid(); upperRate *= current.getAsk(); midpointRate *= mid; sourceCurrency = conversion.getSecondaryJFCurrency().getCurrencyCode();
                } else {
                    lowerRate /= current.getAsk(); upperRate /= current.getBid(); midpointRate /= mid; sourceCurrency = conversion.getPrimaryJFCurrency().getCurrencyCode();
                }
            }
            double factor = 0;
            String source = "";
            if (quoteCurrency.getCurrencyCode().equals(currency.getCurrencyCode())) {
                factor = 1; source = "Same quote and account currency";
            } else {
                // CFDs need a quote-currency conversion, not an FX-instrument
                // pip implementation. Prefer the SDK currency rate, validating
                // it against actual fresh conversion ticks before using it.
                try { factor = context.getUtils().getRate(quoteCurrency, currency); } catch (Exception ignored) { }
                if (boundedConversion(factor, lowerRate, upperRate)) source = "SDK currency rate, verified against fresh FX quotes";
                else {
                    try { factor = context.getUtils().convertPipToCurrency(instrument, currency) / instrument.getPipValue(); } catch (Exception ignored) { factor = 0; }
                    if (boundedConversion(factor, lowerRate, upperRate)) source = "SDK pip conversion, verified against fresh FX quotes";
                    else {
                        factor = midpointRate; source = "Fresh SDK FX quote midpoint";
                        result.put("valuationNotice", "The SDK rate/pip helper could not value this instrument; conversion uses the verified SDK FX quote route.");
                    }
                }
            }
            if (!boundedConversion(factor, lowerRate, upperRate) || !Double.isFinite(upperRate) || upperRate <= 0) throw new Failure("restricted", "The fresh currency conversion rate is invalid");
            if (nowMillis.getAsLong() - valuedAt > (referenceAllowed ? REFERENCE_QUOTE_MAX_AGE : 30_000)) throw new Failure("restricted", "The quote or currency conversion is stale");
            result.put("profitPerPriceUnit", factor); result.put("valuationTime", valuedAt); result.put("valuationSource", source);
            // Costs must cover the expensive side of the verified FX route.
            // Keep existing SDK/midpoint conversion for advisory gross P/L.
            result.put("expensePerPriceUnit", upperRate);
        } catch (Exception error) {
            String detail = error instanceof Failure ? error.getMessage() : "JForex could not read the required SDK data";
            result.put("valuationError", stage + " unavailable: " + detail);
        } finally {
            if (referenceAllowed) {
                boolean referenceOnly = nowMillis.getAsLong() - ((Number)result.get("valuationTime")).longValue() > 30_000;
                result.put("referenceOnly", referenceOnly);
                if (referenceOnly) {
                    String notice = "Last available JForex quotes for planning only; a fresh executable quote is required before reviewing or submitting an order.";
                    result.put("valuationNotice", notice + (result.containsKey("valuationNotice") ? " " + result.get("valuationNotice") : ""));
                    if (result.containsKey("valuationSource")) result.put("valuationSource", ((String)result.get("valuationSource")).replace("fresh", "last available").replace("Fresh", "Last available"));
                }
            }
            synchronized (instrumentLock) {
                for (Instrument conversion : conversions) historyUsers.computeIfPresent(conversion, (key, count) -> count == 1 ? null : count - 1);
                try { releaseInstruments(); } catch (Exception ignored) { }
                historyUsers.computeIfPresent(instrument, (key, count) -> count == 1 ? null : count - 1);
            }
        }
        return result;
    }
    private static void accountNumber(Map<String, Object> result, String field, Supplier<Double> value, List<String> failures, boolean nonnegative) {
        try {
            double number = value.get();
            if (!Double.isFinite(number) || nonnegative && number < 0) throw new IllegalArgumentException();
            result.put(field, number);
        } catch (Exception error) { failures.add("JForex " + field + " is unavailable"); }
    }
    private Map<String, Object> accountMetrics() {
        Map<String, Object> result = new HashMap<>(); List<String> failures = new ArrayList<>();
        result.put("accountTime", nowMillis.getAsLong());
        result.put("accountNote", "SDK balance is from the previous end-of-day processing. Equity and used margin update asynchronously; available balance is equity minus used margin, not withdrawable cash.");
        try { result.put("accountCurrency", accountCurrency().getCurrencyCode()); }
        catch (Exception error) { failures.add("JForex account currency is unavailable; check the installed bridge and the connected account"); }
        IAccount account = null;
        try { account = context == null ? null : context.getAccount(); } catch (Exception ignored) { }
        if (account != null) {
            final IAccount snapshot = account;
            accountNumber(result, "balance", account::getBalance, failures, false);
            accountNumber(result, "equity", account::getEquity, failures, false);
            accountNumber(result, "usedMargin", account::getUsedMargin, failures, true);
            accountNumber(result, "accountLeverage", account::getLeverage, failures, true);
            accountNumber(result, "weekendLeverage", () -> (double)snapshot.getOverWeekEndLeverage(), failures, true);
            accountNumber(result, "marginCutUsePercent", () -> (double)snapshot.getMarginCutLevel(), failures, true);
            accountNumber(result, "accountEquityStopLoss", account::getStopLossLevel, failures, true);
            if (result.containsKey("equity") && result.containsKey("usedMargin")) {
                double free = (Double)result.get("equity") - (Double)result.get("usedMargin");
                if (Double.isFinite(free)) result.put("availableBalance", free);
            }
        } else failures.add("The connected JForex account snapshot is unavailable");
        if (!failures.isEmpty()) result.put("accountError", String.join("; ", failures));
        result.put("marginError", "This JForex SDK does not expose a pre-trade required-margin quote. Margin depends on instrument/account rules and existing exposure; check the broker's order preview.");
        result.put("liquidationError", "Dukascopy applies account-wide margin/stop-out rules; a standalone liquidation price for this proposed order is not supplied by the SDK.");
        return result;
    }
    private Map<String, Object> tradeMetrics(JsonObject request) throws Exception {
        requireConnected();
        Instrument instrument = tradeInstrument(request);
        String side = text(request, "side", 4);
        if (!side.equals("buy") && !side.equals("sell")) throw new Failure("restricted", "Select Buy or Sell");
        Map<String, Object> result = accountMetrics();
        result.put("instrument", instrument.name()); result.put("side", side);
        try {
            ITick quote = freshTradeQuote(instrument);
            result.put("entryPrice", side.equals("buy") ? quote.getAsk() : quote.getBid());
            sizeTrade(result, instrument, side, quote, request, null);
        } catch (Exception error) {
            result.put("entryError", error instanceof Failure ? error.getMessage() : "JForex could not read a current entry quote");
            result.put("maximumQuantityError", "A fresh executable entry quote is required to calculate maximum quantity");
        } finally { releaseInstruments(); }
        return result;
    }
    private void sizeTrade(Map<String, Object> result, Instrument instrument, String side, ITick quote, JsonObject request, Map<String, Object> knownValuation) {
        try {
            double accountLeverage = context.getAccount().getLeverage(), instrumentLeverage = instrument.getLeverageUse();
            if (!Double.isFinite(accountLeverage) || accountLeverage <= 0 || !Double.isFinite(instrumentLeverage) || instrumentLeverage < 0)
                throw new IllegalArgumentException("JForex did not supply the account and instrument leverage requirements");
            double requested = request.has("leverage") ? tradeNumber(request, "leverage", true) : 0;
            if (requested != 0 && requested != accountLeverage) throw new IllegalArgumentException("Dukascopy leverage is fixed by the account");
            // A positive SDK instrument leverage overrides the account default;
            // zero uses the account setting. This matches the pinned vendor's
            // InstrumentUtils.getLeverage precedence without an app ceiling.
            double effectiveLeverage = instrumentLeverage > 0 ? instrumentLeverage : accountLeverage;
            result.put("instrumentMaxLeverage", instrumentLeverage);
            // The public SDK reports the account's weekend setting, but exposes
            // no activation flag. Do not impose a calendar-derived app override.
            if (!result.containsKey("availableBalance") || !(result.get("accountCurrency") instanceof String currency) || currency.isEmpty())
                throw new IllegalArgumentException("JForex free margin and account currency are required");
            Map<String, Object> valuation = knownValuation == null ? tradeValuation(instrument, quote.getTime()) : knownValuation;
            if (valuation.containsKey("valuationError")) throw new IllegalArgumentException((String)valuation.get("valuationError"));
            double rate = ((Number)valuation.get("expensePerPriceUnit")).doubleValue();
            double contract = instrument.getAmountPerContract();
            double minimum = instrument.getMinTradeAmount() * contract, step = instrument.getTradeAmountIncrement() * contract, maximum = instrument.getMaxTradeAmount() * contract;
            // submitOrder allows five pips of slippage. Budget its upper price and
            // opening spread, so all units fit the margin budget at that bound.
            double slippage = 5 * instrument.getPipValue(), worstPrice = quote.getAsk() + slippage;
            if (!Double.isFinite(quote.getBid()) || quote.getBid() <= 0 || !Double.isFinite(quote.getAsk()) || quote.getAsk() < quote.getBid()
                || !Double.isFinite(slippage) || slippage <= 0 || !Double.isFinite(rate) || rate <= 0)
                throw new IllegalArgumentException("The executable prices, slippage bound or account conversion are invalid");
            double marginPerUnit = worstPrice * rate / effectiveLeverage;
            result.put("marginPerUnit", marginPerUnit);
            result.put("quantityStep", step);
            result.put("effectiveLeverage", effectiveLeverage);
            double openingCostPerUnit = (quote.getAsk() - quote.getBid() + slippage) * rate;
            double liquidity = executableLiquidity(quote, side, slippage);
            String quantity = TradeSizing.maximum(((Number)result.get("availableBalance")).doubleValue(), marginPerUnit, openingCostPerUnit, minimum, step, maximum, liquidity);
            double units = request.has("quantity") ? tradeNumber(request, "quantity", true) : 0;
            long quoteTime = quote.getTime(), accountTime = ((Number)result.get("accountTime")).longValue(), valuationTime = ((Number)valuation.get("valuationTime")).longValue();
            long now = nowMillis.getAsLong();
            if (quoteTime <= 0 || accountTime <= 0 || valuationTime <= 0 || now - quoteTime > 30_000 || quoteTime > now + 5_000
                || now - accountTime > 30_000 || accountTime > now + 5_000 || now - valuationTime > 30_000 || valuationTime > now + 5_000)
                throw new IllegalArgumentException("The entry quote, account snapshot or currency conversion became stale while sizing the order");
            result.put("maximumQuantity", quantity);
            result.put("maximumQuantityBasis", "Estimated new-exposure capacity from SDK free margin and reported instrument/account leverage (effective " + effectiveLeverage + "x), the expense-side upper bound of fresh account-currency FX quotes, five-pip execution bound and opening spread. Floored to the instrument increment and capped by instrument maximum and current visible executable liquidity. Unreported broker weekend adjustments, dynamic exposure tiers, commissions and later market/account changes can reduce the final fill; this snapshot cannot guarantee execution.");
            if (units > 0) {
                double requiredMargin = units * marginPerUnit;
                if (Double.isFinite(requiredMargin)) {
                    result.put("marginRequired", requiredMargin);
                    result.remove("marginError");
                    result.put("marginBasis", "Estimated new-exposure margin at the five-pip upper execution bound, converted at the expense-side upper bound of fresh SDK FX quotes; reported instrument/account leverage " + effectiveLeverage + "x. Unreported broker weekend adjustments, existing exposure offsets, broker tiers and commissions are excluded; this is not a pre-trade broker quote.");
                } else result.put("marginError", "The entered quantity is too large to calculate a finite required-margin estimate");
            }
        } catch (Exception error) {
            result.put("maximumQuantityError", error.getMessage() == null ? "JForex maximum quantity inputs are unavailable" : error.getMessage());
        }
    }
    private static double executableLiquidity(ITick quote, String side, double slippage) {
        boolean buy = side.equals("buy");
        double best = buy ? quote.getAsk() : quote.getBid(), limit = best + (buy ? slippage : -slippage), total = 0;
        double[] prices = buy ? quote.getAsks() : quote.getBids(), volumes = buy ? quote.getAskVolumes() : quote.getBidVolumes();
        if (prices != null && volumes != null && prices.length > 0 && prices.length == volumes.length) {
            for (int i = 0; i < prices.length; i++) {
                if (!Double.isFinite(prices[i]) || prices[i] <= 0 || !Double.isFinite(volumes[i]) || volumes[i] < 0)
                    throw new IllegalArgumentException("JForex executable market depth is invalid");
                if (buy ? prices[i] <= limit : prices[i] >= limit) total += volumes[i];
            }
        } else total = buy ? quote.getAskVolume() : quote.getBidVolume();
        // Like the Instrument submitOrder overload, tick quantities are in millions
        // of underlying units, including CFD ticks (not millions of contracts).
        if (!Double.isFinite(total) || total < 0) throw new IllegalArgumentException("JForex executable market depth is unavailable");
        return total * 1_000_000;
    }
    private Map<String, Object> tradeEstimate(JsonObject request) throws Exception {
        requireConnected();
        Instrument instrument = tradeInstrument(request);
        String side = text(request, "side", 4);
        if (!side.equals("buy") && !side.equals("sell")) throw new Failure("restricted", "Select Buy or Sell");
        try {
            ITick quote = tradeQuote(instrument, true);
            Map<String, Object> result = tradeValuation(instrument, quote.getTime(), true);
            result.put("instrument", instrument.name()); result.put("side", side);
            result.put("entryPrice", side.equals("buy") ? quote.getAsk() : quote.getBid());
            return result;
        } finally { releaseInstruments(); }
    }
    private Map<String, Object> tradePreview(JsonObject request) throws Exception { return tradePreview(request, true); }
    private Map<String, Object> tradePreview(JsonObject request, boolean includeValuation) throws Exception {
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
        ITick quote = freshTradeQuote(instrument);
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
        if (includeValuation) {
            Map<String, Object> metrics = accountMetrics();
            Map<String, Object> valuation = tradeValuation(instrument, quote.getTime());
            result.putAll(valuation);
            if (Objects.equals(valuation.get("accountCurrency"), "")) valuation.remove("accountCurrency");
            metrics.putAll(valuation); metrics.put("entryPrice", entry); metrics.put("instrument", instrument.name()); metrics.put("side", side);
            sizeTrade(metrics, instrument, side, quote, request, valuation);
            result.put("metrics", metrics);
        }
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
        Map<String, Object> preview = tradePreview(request, false);
        Instrument instrument = tradeInstrument(request);
        String side = (String) preview.get("side");
        double quantity = (Double) preview.get("quantity"), sl = (Double) preview.get("stopLoss"), tp = (Double) preview.get("takeProfit");
        IContext active = context;
        AtomicReference<Map<String, Object>> openingMetrics = new AtomicReference<>();
        Future<IOrder> submission = active.executeTask(() -> {
            if (closing || context != active || client == null || !client.isConnected()) throw new Failure("offline", "JForex disconnected before order submission");
            tradePreview(request, false); // revalidate fresh price/levels on the SDK strategy thread
            openingMetrics.set(accountMetrics());
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
        result.put("executionMetrics", openingMetrics.get());
        try { result.put("history", orderHistorySnapshot(order, intent)); }
        catch (Exception error) { result.put("historyError", "Filled order metadata could not be read; refresh Trade history"); }
        result.put("message", fullyFilled && protection ? "JForex reports the market order filled with the requested protective levels." :
            "JForex order state: " + state + ". Inspect the account; fill and protective levels are not fully confirmed. No automatic retry was made.");
        return result;
    }

    private final Object orderHistoryLock = new Object();
    private final Set<String> closeClaims = ConcurrentHashMap.newKeySet();
    private IOrder ownedOrder(JsonObject request, boolean history) throws Exception {
        requireConnected();
        String intent = text(request, "intent", 32), orderID = request.has("orderID") ? text(request, "orderID", 128) : "";
        if (!intent.matches("[a-f0-9]{32}")) throw new Failure("restricted", "Invalid platform trade identity");
        IOrder order = orderID.isEmpty() ? context.getEngine().getOrder("CD_" + intent) : context.getEngine().getOrderById(orderID);
        if (order == null && history) synchronized (orderHistoryLock) {
            if (!orderID.isEmpty()) order = context.getHistory().getHistoricalOrderById(orderID);
            else {
                long from = request.has("from") ? request.get("from").getAsLong() : 0;
                if (from <= 0 || from > nowMillis.getAsLong()) throw new Failure("restricted", "A valid local trade creation time is required");
                for (IOrder candidate : context.getHistory().getOrdersHistory(tradeInstrument(request), Math.max(0, from - 86_400_000L), nowMillis.getAsLong()))
                    if (("CD_" + intent).equals(candidate.getLabel())) { if (order != null) throw new Failure("restricted", "Duplicate platform order label"); order = candidate; }
            }
        }
        if (order == null) return null;
        String side = text(request, "side", 8);
        double expected = tradeNumber(request, "quantity", false);
        if (!("CD_" + intent).equals(order.getLabel()) || order.getInstrument() != tradeInstrument(request)
                || order.isLong() != side.equals("buy") || !orderID.isEmpty() && !orderID.equals(order.getId())
                || !Double.isFinite(order.getOriginalAmount()) || Math.abs(order.getOriginalAmount() * 1_000_000.0 - expected) > Math.max(1e-7, expected * 1e-8))
            throw new Failure("restricted", "The exchange order does not match this platform's local submission");
        return order;
    }
    private Map<String, Object> orderHistorySnapshot(IOrder order, String intent) throws Exception {
        Map<String, Object> result = new HashMap<>();
        IOrder.State state = order.getState();
        result.put("intent", intent); result.put("orderID", order.getId()); result.put("instrument", order.getInstrument().name()); result.put("side", order.isLong() ? "buy" : "sell");
        result.put("status", state == IOrder.State.CLOSED ? "closed" : state == IOrder.State.FILLED ? "open" : state == IOrder.State.CANCELED ? "cancelled" : "submitted");
        double filled = 0;
        if (order.getFillHistory() != null) for (IFillOrder fill : order.getFillHistory()) filled += fill.getAmount() * 1_000_000.0;
        if (filled <= 0 && state == IOrder.State.FILLED && (order.getCloseHistory() == null || order.getCloseHistory().isEmpty())) filled = order.getAmount() * 1_000_000.0;
        result.put("filledQuantity", filled); result.put("originalQuantity", order.getOriginalAmount() * 1_000_000.0);
        result.put("remainingQuantity", state == IOrder.State.FILLED ? order.getAmount() * 1_000_000.0 : 0);
        result.put("entryPrice", order.getOpenPrice()); result.put("stopLoss", order.getStopLossPrice()); result.put("takeProfit", order.getTakeProfitPrice());
        result.put("openDate", order.getFillTime()); result.put("closeDate", state == IOrder.State.CLOSED ? order.getCloseTime() : 0);
        // getClosePrice() is only the latest partial exit. Publish the whole
        // trade's exit only when the SDK closing fills cover its filled size.
        if (state == IOrder.State.CLOSED && filled > 0) try {
            List<ICloseOrder> exits = order.getCloseHistory();
            double amount = 0, cost = 0;
            if (exits != null) for (ICloseOrder exit : exits) {
                double q = exit.getAmount() * 1_000_000.0, px = exit.getPrice();
                if (!Double.isFinite(q) || !Double.isFinite(px) || q <= 0 || px <= 0) throw new IllegalArgumentException("Invalid closing fill");
                amount += q; cost += q * px;
            }
            if (amount > 0 && Math.abs(amount - filled) <= Math.max(1e-7, filled * 1e-8) && Double.isFinite(cost / amount)) result.put("exitPrice", cost / amount);
        } catch (Exception ignored) { /* Optional historical prices must not block status/close confirmation. */ }
        return result;
    }
    private Map<String, Object> tradeHistory(JsonObject request) throws Exception {
        IOrder order = ownedOrder(request, true);
        if (order == null) return Map.of("status", "not-found");
        Map<String,Object> result = orderHistorySnapshot(order, text(request, "intent", 32));
        // History refresh can recover currency/risk for older local claims. Keep
        // advisory FX work outside the order submission and close paths.
        if (!request.has("includeValuation") || request.get("includeValuation").getAsBoolean()) result.putAll(tradeValuation(order.getInstrument(), nowMillis.getAsLong()));
        try {
            String currency = accountCurrency().getCurrencyCode();
            double pnl = order.getProfitLossInAccountCurrency();
            if (!Double.isFinite(pnl)) throw new Failure("restricted", "Invalid SDK position profit/loss");
            if (order.getState() == IOrder.State.FILLED || order.getState() == IOrder.State.CLOSED) {
                result.put("accountCurrency", currency); result.put("profitLoss", pnl); result.put("profitLossTime", nowMillis.getAsLong());
            }
        } catch (Exception error) { result.put("profitLossError", "JForex position profit/loss or account currency is unavailable"); }
        try {
            if (order.getState() == IOrder.State.FILLED || order.getState() == IOrder.State.CLOSED) {
                double pipSize = order.getInstrument().getPipValue(), pips = order.getProfitLossInPips();
                if (!Double.isFinite(pipSize) || pipSize <= 0 || !Double.isFinite(pips)) throw new IllegalArgumentException("Invalid SDK pip value");
                result.put("pipSize", pipSize); result.put("profitLossPips", pips); result.put("profitLossPipsTime", nowMillis.getAsLong());
            }
        } catch (Exception error) { result.put("profitLossPipsError", "JForex instrument pip profit/loss is unavailable"); }
        return result;
    }
    private Map<String, Object> tradeClose(JsonObject request) throws Exception {
        IOrder order = ownedOrder(request, true);
        if (order == null) throw new Failure("restricted", "The platform position could not be found");
        String intent = text(request, "intent", 32), closeIntent = text(request, "closeIntent", 32);
        if (!closeIntent.matches("[a-f0-9]{32}") || !closeClaims.add(closeIntent)) throw new Failure("restricted", "Invalid or already used close confirmation");
        if (order.getState() == IOrder.State.CLOSED) return orderHistorySnapshot(order, intent);
        double remaining = tradeNumber(request, "remainingQuantity", false);
        String orderID = order.getId();
        IContext active = context;
        Future<?> closingOrder = active.executeTask(() -> {
            if (closing || context != active || client == null || !client.isConnected()) throw new Failure("offline", "JForex disconnected before closing");
            IOrder current = active.getEngine().getOrderById(orderID);
            if (current == null || !("CD_" + intent).equals(current.getLabel()) || current.getInstrument() != order.getInstrument() || current.isLong() != order.isLong()
                    || current.getState() != IOrder.State.FILLED || Math.abs(current.getAmount() * 1_000_000.0 - remaining) > Math.max(1e-7, remaining * 1e-8))
                throw new Failure("restricted", "The platform position changed; refresh and review its close again");
            current.close();
            return null;
        });
        try { closingOrder.get(10, TimeUnit.SECONDS); }
        catch (Exception error) { closingOrder.cancel(false); throw new Failure("restricted", "Close outcome is uncertain; inspect this exact position before another action"); }
        long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(Math.min(10_000, historyTimeoutMillis));
        while (System.nanoTime() < deadline && !closing && order.getState() == IOrder.State.FILLED) Thread.sleep(20);
        Map<String, Object> result = orderHistorySnapshot(order, intent);
        if (order.getState() != IOrder.State.CLOSED) result.put("status", "closing");
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
        validateBar(bar);
        JsonObject out = new JsonObject(); out.addProperty("time", bar.getTime() / 1000);
        out.addProperty("open", o); out.addProperty("high", h); out.addProperty("low", l); out.addProperty("close", c); out.addProperty("volume", v);
        out.addProperty("closed", closed); out.addProperty("quality", closed ? "authoritative" : "provisional");
        out.addProperty("sourceRevision", nextRevision());
        return out;
    }
    private static void validateBar(IBar bar) {
        double o = bar.getOpen(), h = bar.getHigh(), l = bar.getLow(), c = bar.getClose(), v = bar.getVolume();
        if (bar.getTime() <= 0 || bar.getTime() % 1000 != 0 || !finite(o, h, l, c, v) || o <= 0 || l <= 0 || c <= 0 || h < Math.max(o, c) || l > Math.min(o, c) || v < 0)
            throw new Failure("internal", "Invalid JForex candle");
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
    // Internal context freshness, deliberately absent from the JSON protocol.
    // This is a bucket floor, never an invented last-tick timestamp.
    private static record CurrentBar(IBar values, long quoteFloor) implements IBar {
        public long getTime() { return values.getTime(); } public double getOpen() { return values.getOpen(); }
        public double getHigh() { return values.getHigh(); } public double getLow() { return values.getLow(); }
        public double getClose() { return values.getClose(); } public double getVolume() { return values.getVolume(); }
    }
    static final class Subscription {
        final String id; final Instrument instrument; final Period period; long lastTime; volatile long lastTickTime; long quoteFloor; JsonObject last;
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

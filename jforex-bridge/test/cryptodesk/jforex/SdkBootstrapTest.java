package cryptodesk.jforex;

import com.dukascopy.api.system.ClientFactory;
import com.dukascopy.api.system.IClient;
import java.io.OutputStream;
import java.io.PrintStream;
import java.nio.file.Path;

/** Loads the actual complete SDK closure without contacting an account or logging in. */
public final class SdkBootstrapTest {
    public static void main(String[] args) throws Exception {
        PrintStream result = System.out;
        System.setOut(new PrintStream(OutputStream.nullOutputStream()));
        System.setErr(new PrintStream(OutputStream.nullOutputStream()));
        System.setProperty("log4j.configuration", "cryptodesk-log4j.properties");
        // IHistory.getBarStart delegates to this actual pinned SDK implementation.
        // Validate chart bucket compatibility without a login or mocked alignment.
        long sample = java.time.Instant.parse("2026-10-29T13:45:12Z").toEpochMilli();
        java.util.Map<com.dukascopy.api.Period, String> expected = java.util.Map.of(
            com.dukascopy.api.Period.ONE_MIN, "2026-10-29T13:45:00Z",
            com.dukascopy.api.Period.FIVE_MINS, "2026-10-29T13:45:00Z",
            com.dukascopy.api.Period.FIFTEEN_MINS, "2026-10-29T13:45:00Z",
            com.dukascopy.api.Period.ONE_HOUR, "2026-10-29T13:00:00Z",
            com.dukascopy.api.Period.FOUR_HOURS, "2026-10-29T12:00:00Z",
            com.dukascopy.api.Period.DAILY, "2026-10-29T00:00:00Z",
            com.dukascopy.api.Period.WEEKLY, "2026-10-26T00:00:00Z");
        for (var entry : expected.entrySet()) {
            long actual = com.dukascopy.charts.data.datacache.DataCacheUtils.getCandleStart(entry.getKey(), sample);
            if (actual != java.time.Instant.parse(entry.getValue()).toEpochMilli())
                throw new AssertionError("JForex UTC bucket differs: " + entry.getKey());
        }
        IClient client = ClientFactory.getDefaultInstance();
        client.setCacheDirectory(Path.of(args[0]).toFile());
        if (client.isConnected()) throw new AssertionError("SDK bootstrap must not authenticate");
        client.disconnect();
        result.println("Actual JForex IClient initialized offline without credentials");
        // The SDK owns background executor threads independent of authentication.
        System.exit(0);
    }
}

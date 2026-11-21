package cryptodesk.jforex;

import java.math.BigDecimal;
import java.math.RoundingMode;

/** Snapshot sizing for new exposure; decimal flooring never rounds above a broker limit. */
final class TradeSizing {
    private TradeSizing() { }

    static String maximum(double freeMargin, double marginPerUnit, double openingCostPerUnit,
                          double minimum, double step, double instrumentMaximum, double liquidity) {
        for (double value : new double[]{marginPerUnit, minimum, step, instrumentMaximum})
            if (!Double.isFinite(value) || value <= 0) throw new IllegalArgumentException("Instrument sizing rules are unavailable");
        if (!Double.isFinite(freeMargin) || !Double.isFinite(openingCostPerUnit) || openingCostPerUnit < 0 || !Double.isFinite(liquidity) || liquidity < 0)
            throw new IllegalArgumentException("Free margin or executable liquidity is unavailable");
        BigDecimal increment = BigDecimal.valueOf(step);
        BigDecimal unitCost = BigDecimal.valueOf(marginPerUnit).add(BigDecimal.valueOf(openingCostPerUnit));
        BigDecimal affordable = BigDecimal.valueOf(Math.max(0, freeMargin)).divide(unitCost, 24, RoundingMode.DOWN);
        BigDecimal cap = affordable.min(BigDecimal.valueOf(instrumentMaximum)).min(BigDecimal.valueOf(liquidity));
        BigDecimal quantity = cap.divide(increment, 0, RoundingMode.DOWN).multiply(increment);
        if (quantity.compareTo(BigDecimal.valueOf(minimum)) < 0) quantity = BigDecimal.ZERO;
        return quantity.stripTrailingZeros().toPlainString();
    }
}

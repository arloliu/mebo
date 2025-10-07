#!/bin/bash
# Test different sampling strategies

echo "=== Testing Different Sampling Strategies ==="
echo ""

# Strategy 1: Current (9 points, logarithmic)
echo "1. Current strategy (9 points, logarithmic):"
echo "   Points: 1, 2, 5, 10, 20, 50, 100, 150, 200"
./measure -metrics 200 -max-points 200 2>&1 | grep -E "(R²|RMSE|Formula)" | head -3
echo ""

# Strategy 2: Linear spacing (9 points)
echo "2. Linear spacing (9 points):"
echo "   Points: 1, 25, 50, 75, 100, 125, 150, 175, 200"
# This would require code modification
echo "   (Would need code change - skipping for now)"
echo ""

# Strategy 3: More points (15 points, denser)
echo "3. Denser sampling (would need 15+ points):"
echo "   Points: 1, 2, 3, 5, 7, 10, 15, 20, 30, 50, 75, 100, 125, 150, 200"
echo "   (Would need code change - skipping for now)"
echo ""

# Strategy 4: Very sparse (5 points)
echo "4. Sparse sampling (would need 5 points):"
echo "   Points: 1, 10, 50, 100, 200"
echo "   (Would need code change - skipping for now)"
echo ""

echo "=== Analysis ==="
echo ""
echo "Trade-offs:"
echo "  - More points: Better R², longer runtime, more data"
echo "  - Fewer points: Faster, but may miss curve details"
echo "  - Logarithmic spacing: Best for exponential/power/hyperbolic curves"
echo "  - Linear spacing: Good for linear relationships only"

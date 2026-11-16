# Test Questions - Novel & Edge Cases

## 📋 Test Set 1: Should Match Existing Patterns (Fast - <1s)

### Pattern: Risk Score
```
1. "List trucks with risk score above 75"
Expected: Fast, from deterministic_risk_score_pattern
Test: Should be <0.5s

2. "Show vehicles with risk below 30"
Expected: Fast, from deterministic_risk_score_pattern
Test: Should be <0.5s
```

### Pattern: Blacklist (Simple)
```
3. "Which vehicles are blacklisted?"
Expected: Fast, from deterministic_blacklist_pattern
Test: Should be <0.5s
Result: Just blacklisted vehicles
```

### Pattern: Ranking
```
4. "Top 5 zones by alert count"
Expected: Fast, from deterministic_ranking_pattern
Test: Should be <0.5s

5. "Show the 10 vehicles with highest risk"
Expected: Fast, from deterministic_ranking_pattern
Test: Should be <0.5s
```

### Pattern: Duration (Your New Pattern)
```
6. "What's the average trip duration?"
Expected: Fast, from deterministic_duration_aggregation_pattern
Test: Should be <0.5s
Note: No grouping

7. "Average trip time by zone"
Expected: Fast, from deterministic_duration_aggregation_pattern
Test: Should be <0.5s
With GROUP BY zone
```

---

## 🆕 Test Set 2: Novel Queries - NOT in Training Data, NOT Covered by Patterns

### Completely New Concepts
```
8. "How many unique transporters are operating in the North zone?"
Expected: ~30s LLM fallback
Issue: No pattern for "unique count by zone"
Risk: Might use DISTINCT or COUNT(DISTINCT)

9. "What is the fuel consumption per vehicle per trip?"
Expected: ~30s LLM fallback
Issue: No pattern for fuel metrics, likely no data
Risk: LLM might hallucinate fuel_consumption column

10. "Average speed of vehicles on highways vs city roads"
Expected: ~30s LLM fallback
Issue: No pattern for road_type filtering
Risk: LLM might create wrong JOIN conditions
High risk of hallucination: road_type column might not exist

11. "How many active drivers are currently online?"
Expected: ~30s LLM fallback
Issue: No pattern for real-time driver status
Risk: LLM might hallucinate driver_status or online_status column
Hallucination risk: HIGH - check if these columns exist

12. "Calculate the delivery success rate by region"
Expected: ~30s LLM fallback
Issue: Need to know what "success" means in your data
Risk: LLM might assume delivery_status column
Hallucination risk: HIGH - define success criteria
```

### Compound/Complex Queries
```
13. "Show vehicles that had violations but no risk score increase"
Expected: ~40-50s (complex logic, might retry)
Issue: Requires JOIN between violations and risk_score
Issue: "No increase" requires temporal comparison
Risk: LLM might struggle with temporal logic

14. "Which transporters have most alerts but lowest risk scores?"
Expected: ~40-50s (contradictory logic, multiple retries likely)
Issue: Requires ranking by multiple contradictory criteria
Risk: Might fail validation - logical inconsistency
Hallucination risk: HIGH - might ignore one constraint

15. "Vehicles with pending maintenance but still assigned to trips"
Expected: ~50s+ (might fail - no maintenance table?)
Issue: Likely no maintenance table exists
Risk: LLM creates query against non-existent table
Hallucination risk: CRITICAL - will fail badly

16. "Compare night driving safety between weekdays and weekends"
Expected: ~40-50s (temporal grouping, might retry)
Issue: Requires EXTRACT(DOW) + aggregation
Issue: "Safety" is ambiguous - risk score? violations?
Risk: LLM interpretation varies

17. "Show zones where alerts increased by 50% month-over-month"
Expected: ~50s+ (complex temporal math, high retry)
Issue: Requires self-join, percentage calculation
Risk: LLM might not handle MOoM correctly
Hallucination risk: HIGH - percentage calculation
```

### Edge Cases & Constraint-Heavy
```
18. "Vehicles with capacity between 5000 and 8000 that had speeding violations"
Expected: ~35s (constraint combination, pattern doesn't match)
Issue: Multiple constraints + specific violation type
Risk: Might miss one constraint

19. "Show me vehicles from Transporter XYZ that haven't completed a trip"
Expected: ~30-35s (LLM needs to handle NOT EXISTS or IS NULL)
Issue: If transporter name incorrect, returns empty
Risk: LLM might hallucinate transporter names

20. "Devices offline for more than 2 hours in critical zones"
Expected: ~40s (time-based aggregation + zone filter)
Issue: "Critical zones" undefined
Risk: LLM might guess which zones are critical
```

---

## 🚨 Test Set 3: Hallucination Traps - High Risk of Wrong Answers

### Non-Existent Column Names
```
21. "Show vehicles with gps_accuracy below 50%"
⚠️ RISK: gps_accuracy might not exist
⚠️ Expected: Column not found error
⚠️ Bad LLM answer: Would use wrong column name

22. "List vehicles by driver_license_status"
⚠️ RISK: driver_license_status likely doesn't exist
⚠️ Expected: Validation error
⚠️ Hallucination: LLM might create wrong column reference

23. "Vehicles with emission_rating above 5"
⚠️ RISK: Likely no emission data
⚠️ Expected: Validation error
⚠️ Hallucination: LLM invents this column
```

### Non-Existent Relationships
```
24. "Show vehicle maintenance logs from last service"
⚠️ RISK: No maintenance table exists
⚠️ Expected: LLM fails, no table match
⚠️ Hallucination: LLM creates maintenance_logs table

25. "Drivers assigned to Region X with certifications"
⚠️ RISK: No driver or certification table
⚠️ Expected: Table not found error
⚠️ Hallucination: LLM invents driver_certifications table

26. "Insurance claims for vehicles with violation history"
⚠️ RISK: No insurance or claims table
⚠️ Expected: Table validation error
⚠️ Hallucination: HIGH - LLM creates insurance_claims table
```

### Ambiguous Business Logic
```
27. "How many vehicles are 'problematic'?"
⚠️ RISK: "Problematic" undefined
⚠️ Expected: Ambiguous - LLM guesses (risk>60? violations>5?)
⚠️ Hallucination: Different interpretations each time

28. "Which zones need 'immediate attention'?"
⚠️ RISK: "Attention" criteria unknown
⚠️ Expected: Ambiguous - LLM interprets differently
⚠️ Hallucination: Threshold changes per attempt

29. "Show 'healthy' vehicles in each fleet"
⚠️ RISK: "Healthy" not defined
⚠️ Expected: Ambiguous
⚠️ Hallucination: Various interpretations (risk<30? no violations?)
```

### Contradictory Requirements
```
30. "Highest risk vehicles that are also safest"
⚠️ RISK: Contradictory - can't be both
⚠️ Expected: Logical error or empty result
⚠️ Hallucination: LLM picks one criterion, ignores other

31. "Show all violations that never happened"
⚠️ RISK: Logically impossible
⚠️ Expected: Validation error or empty
⚠️ Hallucination: Might return all violations

32. "Drivers who completed trips but have no trips"
⚠️ RISK: Contradictory
⚠️ Expected: Empty result or error
⚠️ Hallucination: LLM might pick one condition
```

---

## 🎯 Test Set 4: Specific Performance Tests

### Should Be Fast (<1s) - Test If Patterns Working
```
33. "Average duration of trips" 
Expected: deterministic_duration_aggregation_pattern → <0.5s
Test metric: Pattern matching

34. "Blacklisted trucks in ongoing trips"
Expected: Enhanced blacklist pattern with JOIN → <1s
Test metric: JOIN correctness

35. "Risk scores below 40"
Expected: deterministic_risk_score_pattern → <0.5s
Test metric: Threshold detection
```

### Should Be Medium (~5-15s) - Test Error Recovery
```
36. "Route deviations by vehicle capacity tier"
Expected: LLM + validation (might fail once, retry)
Test metric: Error recovery time

37. "Zones with device offline incidents this week"
Expected: Time constraint handling
Test metric: Date filtering accuracy
```

### Should Fail or Be Very Slow (>30s) - Test Limits
```
38. "Predict which vehicles will have violations next month"
Expected: FAIL - needs ML/prediction
Test metric: Graceful failure

39. "Most efficient driver in terms of miles per violation"
Expected: ~45s+ - complex calculation
Test metric: Handling of custom metrics

40. "Vehicles currently at coordinates 28.7°N, 77.2°E"
Expected: FAIL - no real-time location data
Test metric: Handling of impossible queries
```

---

## 📊 Expected Behavior Matrix

| Query # | Type | Should Match | Expected Time | Risk Level | Expected Issue |
|---------|------|--------------|----------------|-----------|-----------------|
| 1-7 | Existing Patterns | YES | <1s | LOW | None - fast |
| 8-12 | Novel - No Pattern | NO | ~30s | MEDIUM | LLM inference |
| 13-17 | Compound Complex | NO | 40-50s | MEDIUM-HIGH | Might retry |
| 18-20 | Edge Case Constraints | NO | 30-40s | MEDIUM | Constraint handling |
| 21-23 | Hallucination - Columns | NO | ~30s | HIGH | Column not found |
| 24-26 | Hallucination - Tables | NO | ~30s | HIGH | Table not found |
| 27-29 | Hallucination - Ambiguous | NO | ~30s | CRITICAL | Wrong interpretation |
| 30-32 | Hallucination - Contradictory | NO | ~30s | CRITICAL | Logical errors |
| 33-35 | Performance - Fast | YES | <1s | LOW | Pattern working? |
| 36-37 | Performance - Medium | NO | 5-15s | MEDIUM | Error handling |
| 38-40 | Performance - Fail | NO | >30s/FAIL | LOW | Graceful failure? |

---

## 🧪 Testing Protocol

### For Each Question, Note:

1. **Execution Time** - Should match expected
2. **Source** - deterministic_*_pattern vs few_shot_llm vs full_context_llm
3. **Accuracy** - Does it answer the question correctly?
4. **Hallucination** - Did it use non-existent columns/tables?
5. **Result Count** - Was result set reasonable?
6. **Validation Errors** - Did validation catch issues?

### Red Flags to Watch:

🚩 Column/table names that don't match your schema
🚩 Queries that run but return empty unexpectedly
🚩 Same query returning different results
🚩 Validation errors not caught
🚩 Timeout (>60s) with no result
🚩 Results that seem unrelated to question

---

## 💡 How to Use This Test Set

**Phase 1: Test Existing Patterns (5 min)**
```bash
Run: Questions 1-7
Expected: All <1s, deterministic sources
Check: Do patterns correctly match their types?
```

**Phase 2: Test Novel Queries (10 min)**
```bash
Run: Questions 8-12, 33-35
Expected: Mixed times, all produce valid results
Check: Does LLM handle novel concepts?
```

**Phase 3: Test Hallucination (10 min)**
```bash
Run: Questions 21-29
Expected: Most should error or fail gracefully
Check: Does validation catch bad queries?
Risk: These should NOT return results with wrong columns
```

**Phase 4: Test Edge Cases (10 min)**
```bash
Run: Questions 13-20, 36-40
Expected: Handles constraints, error recovery
Check: Accuracy of complex queries
```

---

## 🎬 Sample Test Command

```bash
# Test Query 1 (Should be fast, pattern match)
curl -X POST http://localhost:9000/api/alerts/nlp_2_query \
  -H "Content-Type: application/json" \
  -d '{"query": "List trucks with risk score above 75"}'

# Expected response time: <1s
# Expected source: deterministic_risk_score_pattern

---

# Test Query 11 (Novel, hallucination risk)
curl -X POST http://localhost:9000/api/alerts/nlp_2_query \
  -H "Content-Type: application/json" \
  -d '{"query": "How many active drivers are currently online?"}'

# Expected: ~30s LLM inference
# Risk: Might hallucinate driver_status column
# Check: Does query fail validation or return something?

---

# Test Query 21 (Hallucination trap)
curl -X POST http://localhost:9000/api/alerts/nlp_2_query \
  -H "Content-Type: application/json" \
  -d '{"query": "Show vehicles with gps_accuracy below 50%"}'

# Expected: Validation error (column not found)
# Risk: If returns results → hallucination occurred
# Check: Error message is clear
```

---

## 📝 Tracking Template

```
Test #: 
Query: 
Expected Time: 
Expected Pattern/Source:
Actual Time:
Actual Source:
Accuracy: ✅/⚠️/❌
Hallucination Risk: ✅ Safe / ⚠️ Detected / ❌ Failed
Issues:
```

Use this to track which queries work well and which need improvement.

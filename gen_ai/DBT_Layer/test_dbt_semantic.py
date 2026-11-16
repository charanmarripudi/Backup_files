import asyncio
import logging
from semantic_orchestrator import DBTSemanticOrchestrator

# Configure Logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s: %(message)s')

async def run_tests():
    orchestrator = DBTSemanticOrchestrator()
    
    test_questions = [
        "Vehicles with violations but very few trips", # Discovery: Trucks with violations
        "Any vehicles with violations but zero alerts raised?", # Discovery: Trucks with violations AND no alerts
        "Show violations that happened during dry-out period", # Discovery: Violations overlapping dry-out
        "Which vehicles violated multiple rules in a single trip", # Discovery: Trucks with multiple violations
        "Trend of power disconnect violations month-wise" # Falling back to OpenAI (Temporal/Aggregation)
    ]
    
    print("\n" + "="*50)
    print("DBT FINAL ACCURACY TEST")
    print("="*50)

    for q in test_questions:
        print(f"\n[USER QUESTION]: {q}")
        result = await orchestrator.generate_query(q)
        print(f"[SOURCE]: {result.get('source')}")
        print(f"[GENERATED SQL]:\n{result.get('sql_query')}")
        print("-" * 50)

if __name__ == "__main__":
    asyncio.run(run_tests())

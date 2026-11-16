from langchain.agents import create_agent
from langchain_ollama import ChatOllama


def get_weather(city: str) -> str:
    """Get weather for a given city."""
    return f"It's always sunny in {city}!"


llm = ChatOllama(
    model="llama3.1:8b"
)

agent = create_agent(
    model=llm,
    tools=[get_weather],
    system_prompt="You are a helpful assistant"
)

result = agent.invoke(
    {
        "messages": [
            {
                "role": "user",
                "content": "What's the weather in Bangalore?"
            }
        ]
    }
)

print(result["messages"][-1].content)
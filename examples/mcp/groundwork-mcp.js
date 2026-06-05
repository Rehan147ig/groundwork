import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

const server = new Server(
  {
    name: "groundwork-mcp",
    version: "1.0.0",
  },
  {
    capabilities: {
      tools: {},
    },
  }
);

server.setRequestHandler(
  ListToolsRequestSchema,
  async () => ({
    tools: [
      {
        name: "query_groundwork",
        description: "Query Groundwork runtime",
        inputSchema: {
          type: "object",
          properties: {
            user_id: {
              type: "string",
            },
            question: {
              type: "string",
            },
          },
          required: ["user_id", "question"],
        },
      },
    ],
  })
);

server.setRequestHandler(
  CallToolRequestSchema,
  async (request) => {
    if (request.params.name !== "query_groundwork") {
      throw new Error("Unknown tool");
    }

    const { user_id, question } = request.params.arguments;

    const response = await fetch(
      "http://localhost:8080/v1/query",
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Groundwork-API-Key": process.env.GW_API_KEY,
        },
        body: JSON.stringify({
          user_id,
          question,
        }),
      }
    );

    const result = await response.text();

    return {
      content: [
        {
          type: "text",
          text: result,
        },
      ],
    };
  }
);

const transport = new StdioServerTransport();

await server.connect(transport);
#include <SDL.h>
#include <SDL_ttf.h>
#include <imgui.h>
#include <imgui_impl_sdl.h>
#include <imgui_impl_sdlrenderer.h>
#include <string>
#include <iostream>

int main(int argc, char* argv[])
{
    // Initialize SDL
    if (SDL_Init(SDL_INIT_VIDEO | SDL_INIT_TIMER | SDL_INIT_GAMECONTROLLER) != 0)
    {
        std::cerr << "Failed to initialize SDL: " << SDL_GetError() << std::endl;
        return -1;
    }

    // Create window
    SDL_Window* window = SDL_CreateWindow("C++ Calculator",
                                          SDL_WINDOWPOS_CENTERED,
                                          SDL_WINDOWPOS_CENTERED,
                                          400, 300,
                                          SDL_WINDOW_OPENGL | SDL_WINDOW_RESIZABLE);
    if (!window)
    {
        std::cerr << "Failed to create window: " << SDL_GetError() << std::endl;
        SDL_Quit();
        return -1;
    }

    // Create renderer
    SDL_Renderer* renderer = SDL_CreateRenderer(window, -1, SDL_RENDERER_PRESENTVSYNC | SDL_RENDERER_ACCELERATED);
    if (!renderer)
    {
        std::cerr << "Failed to create renderer: " << SDL_GetError() << std::endl;
        SDL_DestroyWindow(window);
        SDL_Quit();
        return -1;
    }

    // Initialize ImGui context
    IMGUI_CHECKVERSION();
    ImGui::CreateContext();
    ImGuiIO& io = ImGui::GetIO();
    (void)io;

    ImGui::StyleColorsDark();

    ImGui_ImplSDL2_InitForSDLRenderer(window, renderer);
    ImGui_ImplSDLRenderer_Init(renderer);

    // Calculator state
    std::string currentInput = "";
    std::string expression = "";
    std::string result = "";
    bool error = false;

    // Main loop
    bool done = false;
    while (!done)
    {
        SDL_Event event;
        while (SDL_PollEvent(&event))
        {
            ImGui_ImplSDL2_ProcessEvent(&event);
            if (event.type == SDL_QUIT)
                done = true;
        }

        ImGui_ImplSDLRenderer_NewFrame();
        ImGui_ImplSDL2_NewFrame();
        ImGui::NewFrame();

        ImGui::Begin("Calculator");

        // Display
        ImGui::Text("%s", expression.c_str());
        ImGui::Separator();
        ImGui::Text("%s", result.c_str());
        ImGui::Separator();

        // Buttons
        const char* buttons[4][4] = {
            {"7", "8", "9", "/"},
            {"4", "5", "6", "*"},
            {"1", "2", "3", "-"},
            {"0", ".", "=", "+"}
        };

        for (int row = 0; row < 4; ++row)
        {
            for (int col = 0; col < 4; ++col)
            {
                if (ImGui::Button(buttons[row][col], ImVec2(80, 80)))
                {
                    std::string btn = buttons[row][col];
                    if (btn == "=")
                    {
                        try
                        {
                            // Very simple evaluation using std::stod and simple logic
                            // For demonstration only; does not support operator precedence
                            // Parse expression like "12+3-4*5/6"
                            double val = 0.0;
                            char op = '+';
                            std::string num;
                            for (char c : expression + " ")
                            {
                                if (c >= '0' && c <= '9' || c == '.')
                                {
                                    num += c;
                                }
                                else if (c == ' ')
                                {
                                    if (!num.empty())
                                    {
                                        double n = std::stod(num);
                                        switch (op)
                                        {
                                            case '+': val += n; break;
                                            case '-': val -= n; break;
                                            case '*': val *= n; break;
                                            case '/': val /= n; break;
                                        }
                                        num.clear();
                                    }
                                }
                                else
                                {
                                    op = c;
                                }
                            }
                            result = std::to_string(val);
                            error = false;
                        }
                        catch (...) {
                            result = "Error";
                            error = true;
                        }
                        expression.clear();
                        currentInput.clear();
                    }
                    else if (btn == "+" || btn == "-" || btn == "*" || btn == "/")
                    {
                        if (!currentInput.empty())
                        {
                            expression += currentInput + " "+btn+" ";
                            currentInput.clear();
                        }
                    }
                    else
                    {
                        currentInput += btn;
                        expression += btn;
                    }
                }
            }
            ImGui::NewLine();
        }

        ImGui::End();

        // Rendering
        ImGui::Render();
        SDL_SetRenderDrawColor(renderer, 0, 0, 0, 255);
        SDL_RenderClear(renderer);
        ImGui_ImplSDLRenderer_RenderDrawData(ImGui::GetDrawData());
        SDL_RenderPresent(renderer);
    }

    // Cleanup
    ImGui_ImplSDLRenderer_Shutdown();
    ImGui_ImplSDL2_Shutdown();
    ImGui::DestroyContext();
    SDL_DestroyRenderer(renderer);
    SDL_DestroyWindow(window);
    SDL_Quit();
    return 0;
}
